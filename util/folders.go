// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package util

import (
	"context"
	"fmt"
	"sync"

	"github.com/openrelik/openrelik-go-client"
	"golang.org/x/sync/errgroup"
)

// rootFolderPageSize is the number of root folders requested per API call
// by page number, and for the first page of a listing.
const rootFolderPageSize = 100

// RootFolderBatchSize is the number of root folders requested per API call
// when following cursors, which stay cheap for the server at any depth. It is
// also the most StreamRootFolders fetches with a limit, in one request.
const RootFolderBatchSize = 1000

// rootFolderFetchers is the number of root folder pages fetched at once from
// servers without cursors.
const rootFolderFetchers = 4

// ListAllRootFolders pages through the root folders endpoint and returns
// every root folder, newest created first. Besides the user's own root
// folders, this includes folders other users shared with them. Servers that
// support cursors are followed page by page, by creation time, which doesn't
// change, so no folder is skipped or repeated. On older servers, the first
// page gives the total and the other pages are fetched concurrently. If
// progress is not nil, it is called after each page with the number of
// folders fetched so far and the total; calls don't overlap.
func ListAllRootFolders(ctx context.Context, client *openrelik.Client, progress func(fetched, total int)) ([]openrelik.Folder, error) {
	// Asking for the default order explicitly tells servers that support
	// cursors apart: they echo the order back.
	first, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.PageOptions{
		Page: 1, PageSize: rootFolderPageSize, Sort: "created_at", Order: "desc",
	})
	if err != nil {
		return nil, err
	}
	if first.Sort == "created_at" && first.Order == "desc" {
		var folders []openrelik.Folder
		err := followRootFolders(ctx, client, first, func(page []openrelik.Folder) error {
			folders = append(folders, page...)
			if progress != nil && first.NextCursor != "" {
				// Only the first page has a total, and a folder shared while
				// listing can take the count past it.
				progress(len(folders), max(len(folders), first.TotalCount))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return folders, nil
	}
	return listRootFoldersByPage(ctx, client, first, progress)
}

// StreamRootFolders lists the root folders sorted by the server by sort
// ("created_at", "updated_at" or "display_name") and order ("asc" or "desc"),
// calling each with every page as it arrives. It returns the total number of
// root folders, also when it fails partway.
//
// With limit > 0, only the first limit folders are fetched, in one request, so
// they are a consistent snapshot; limit must not be above
// RootFolderBatchSize. Without a limit, the server is followed page by page.
// A folder that moves in the order while paging (an update, with updated_at)
// is passed once, where it was first seen, but one that moves before the
// cursor is missed: only use orders where changes move folders to the end,
// such as updated_at ascending.
//
// ok is false if the server doesn't support sorting (older servers). Then
// each isn't called, and the caller should use ListAllRootFolders and sort
// locally instead.
func StreamRootFolders(ctx context.Context, client *openrelik.Client, sort, order string, limit int, each func([]openrelik.Folder) error) (total int, ok bool, err error) {
	if limit > RootFolderBatchSize {
		return 0, false, fmt.Errorf("limit %d is above %d", limit, RootFolderBatchSize)
	}
	pageSize := rootFolderPageSize
	if limit > 0 {
		pageSize = limit
	}
	first, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.PageOptions{
		Page: 1, PageSize: pageSize, Sort: sort, Order: order,
	})
	if err != nil {
		return 0, false, err
	}
	// Never trust an order the server didn't confirm.
	if first.Sort != sort || first.Order != order {
		return 0, false, nil
	}
	if limit > 0 {
		folders := first.Folders[:min(len(first.Folders), limit)]
		if len(folders) == 0 {
			return first.TotalCount, true, nil
		}
		return first.TotalCount, true, each(folders)
	}
	seen := make(map[int]bool)
	return first.TotalCount, true, followRootFolders(ctx, client, first, func(page []openrelik.Folder) error {
		var unseen []openrelik.Folder
		for _, f := range page {
			if !seen[f.ID] {
				seen[f.ID] = true
				unseen = append(unseen, f)
			}
		}
		if len(unseen) == 0 {
			return nil
		}
		return each(unseen)
	})
}

// followRootFolders calls each with the folders of first, then follows its
// cursor through the following pages.
func followRootFolders(ctx context.Context, client *openrelik.Client, first *openrelik.FolderPage, each func([]openrelik.Folder) error) error {
	page := first
	for {
		if len(page.Folders) > 0 {
			if err := each(page.Folders); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		next, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.PageOptions{
			PageSize: RootFolderBatchSize, Sort: first.Sort, Order: first.Order, After: page.NextCursor,
		})
		if err != nil {
			return err
		}
		page = next
	}
}

// listRootFoldersByPage fetches the pages after first by page number, for
// servers without cursors, four at a time.
func listRootFoldersByPage(ctx context.Context, client *openrelik.Client, first *openrelik.FolderPage, progress func(fetched, total int)) ([]openrelik.Folder, error) {
	// Use the page size the server applied, in case it caps the one asked for.
	pageSize := first.PageSize
	if pageSize <= 0 {
		pageSize = rootFolderPageSize
	}
	// Fewer folders than that while there are more means the server applied
	// a smaller page size without saying so.
	if n := len(first.Folders); n > 0 && n < pageSize && n < first.TotalCount {
		pageSize = n
	}
	pages := make([][]openrelik.Folder, 1, max(1, (first.TotalCount+pageSize-1)/pageSize))
	pages[0] = first.Folders
	if len(first.Folders) == 0 || len(first.Folders) >= first.TotalCount {
		return first.Folders, nil
	}
	pages = pages[:cap(pages)]

	// fetched counts distinct folders, so that progress never repeats or goes
	// back when a shifted folder is served twice.
	var mu sync.Mutex
	seen := make(map[int]bool, first.TotalCount)
	fetched, total := 0, first.TotalCount
	count := func(page []openrelik.Folder) {
		for _, f := range page {
			if !seen[f.ID] {
				seen[f.ID] = true
				fetched++
			}
		}
	}
	report := func() {
		if progress != nil {
			// A folder deleted while listing can leave the total behind.
			progress(min(fetched, total), total)
		}
	}
	count(first.Folders)
	report()
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(rootFolderFetchers)
	for i := 1; i < len(pages); i++ {
		g.Go(func() error {
			result, _, err := client.Folders().ListRootFoldersPage(gctx, &openrelik.PageOptions{Page: i + 1, PageSize: pageSize})
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			pages[i] = result.Folders
			count(result.Folders)
			total = max(total, result.TotalCount)
			report()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// The pages are offsets into a list ordered by creation, so a folder
	// created while listing shifts the later pages: it can appear twice, and
	// push the oldest folders past the pages fetched. Those are fetched
	// afterwards, while the last page is full, until the latest total is
	// reached. A short last page means nothing was pushed past it, also when
	// a deleted folder leaves the total behind.
	folders := make([]openrelik.Folder, 0, fetched)
	added := make(map[int]bool, fetched)
	add := func(page []openrelik.Folder) {
		for _, f := range page {
			if !added[f.ID] {
				added[f.ID] = true
				folders = append(folders, f)
			}
		}
	}
	for _, page := range pages {
		add(page)
	}
	last := len(pages[len(pages)-1])
	for page := len(pages) + 1; last >= pageSize && len(folders) < total; page++ {
		result, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.PageOptions{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		add(result.Folders)
		count(result.Folders)
		total = result.TotalCount
		last = len(result.Folders)
		if last > 0 {
			report()
		}
	}
	return folders, nil
}

// MultipleFoldersError is returned by FindOrCreateFolder when more than one
// folder with the requested name already exists.
type MultipleFoldersError struct {
	Name    string
	Matches []openrelik.Folder
}

func (e *MultipleFoldersError) Error() string {
	return fmt.Sprintf("multiple folders named %q already exist", e.Name)
}

// FindOrCreateFolder returns the folder named name in parentID, creating it
// if it doesn't exist. A parentID of 0 means the root, where only folders the
// user owns are considered, since a new root folder would be theirs. Names
// are matched exactly. The returned bool reports whether the folder was
// created. If several folders match, it returns a *MultipleFoldersError.
//
// This is not atomic: the server allows duplicate names, so concurrent calls
// can both create the folder.
func FindOrCreateFolder(ctx context.Context, client *openrelik.Client, parentID int, name string) (*openrelik.Folder, bool, error) {
	var candidates []openrelik.Folder
	if parentID > 0 {
		subfolders, _, err := client.Folders().ListSubFolders(ctx, parentID)
		if err != nil {
			return nil, false, fmt.Errorf("failed to list subfolders of folder %d: %w", parentID, err)
		}
		candidates = subfolders
	} else {
		me, _, err := client.Users().GetMe(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get current user: %w", err)
		}
		roots, err := ListAllRootFolders(ctx, client, nil)
		if err != nil {
			return nil, false, fmt.Errorf("failed to list root folders: %w", err)
		}
		for _, f := range roots {
			if f.User.ID == me.ID {
				candidates = append(candidates, f)
			}
		}
	}

	var matches []openrelik.Folder
	for _, f := range candidates {
		if f.DisplayName == name {
			matches = append(matches, f)
		}
	}
	switch len(matches) {
	case 0:
	case 1:
		return &matches[0], false, nil
	default:
		return nil, false, &MultipleFoldersError{Name: name, Matches: matches}
	}

	var folder *openrelik.Folder
	var err error
	if parentID > 0 {
		folder, _, err = client.Folders().CreateSubFolder(ctx, parentID, name)
	} else {
		folder, _, err = client.Folders().CreateRootFolder(ctx, name)
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to create folder %s: %w", name, err)
	}
	return folder, true, nil
}

// LatestFolder returns the most recently updated of folders, preferring the
// higher (newer) ID when update times are equal. folders must not be empty.
func LatestFolder(folders []openrelik.Folder) *openrelik.Folder {
	latest := &folders[0]
	for i := range folders[1:] {
		f := &folders[i+1]
		if f.UpdatedAt.After(latest.UpdatedAt) || (f.UpdatedAt.Equal(latest.UpdatedAt) && f.ID > latest.ID) {
			latest = f
		}
	}
	return latest
}
