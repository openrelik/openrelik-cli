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

	"github.com/openrelik/openrelik-go-client"
)

// rootFolderPageSize is the number of root folders requested per API call.
const rootFolderPageSize = 100

// ListAllRootFolders pages through the root folders endpoint and returns
// every root folder. Besides the user's own root folders, this includes
// folders other users shared with them.
func ListAllRootFolders(ctx context.Context, client *openrelik.Client) ([]openrelik.Folder, error) {
	var folders []openrelik.Folder
	for page := 1; ; page++ {
		result, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.ListOptions{Page: page, PageSize: rootFolderPageSize})
		if err != nil {
			return nil, err
		}
		folders = append(folders, result.Folders...)
		if len(result.Folders) == 0 || len(folders) >= result.TotalCount {
			return folders, nil
		}
	}
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
		roots, err := ListAllRootFolders(ctx, client)
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
