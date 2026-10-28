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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openrelik/openrelik-go-client"
)

// newFindOrCreateServer serves roots as paginated root folders, subfolders
// of folder 50, and the current user (ID 7). POSTs to create a folder are
// recorded in created as "parentID/name" ("root/name" for root folders).
func newFindOrCreateServer(t *testing.T, roots, subfolders []string, created *[]string) *openrelik.Client {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("/api/v1/users/me/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id": 7}`)
	})
	mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		start := min((page-1)*size, len(roots))
		end := min(page*size, len(roots))
		fmt.Fprintf(w, `{"folders": [%s], "page": %d, "page_size": %d, "total_count": %d}`,
			strings.Join(roots[start:end], ","), page, size, len(roots))
	})
	mux.HandleFunc("/api/v1/folders/50/folders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprintf(w, "[%s]", strings.Join(subfolders, ","))
			return
		}
		var body struct {
			DisplayName string `json:"display_name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		*created = append(*created, "50/"+body.DisplayName)
		fmt.Fprintf(w, `{"id": 900, "display_name": %q}`, body.DisplayName)
	})
	mux.HandleFunc("/api/v1/folders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			DisplayName string `json:"display_name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		*created = append(*created, "root/"+body.DisplayName)
		fmt.Fprintf(w, `{"id": 900, "display_name": %q}`, body.DisplayName)
	})

	client, _ := openrelik.NewClient(server.URL, "test-key")
	return client
}

func folderJSON(id int, name string, owner int) string {
	return fmt.Sprintf(`{"id": %d, "display_name": %q, "user": {"id": %d}}`, id, name, owner)
}

func TestFindOrCreateFolder(t *testing.T) {
	// 150 owned root folders, so "Case 7" (ID 150) is on the second page.
	var manyRoots []string
	for id := 1; id < 150; id++ {
		manyRoots = append(manyRoots, folderJSON(id, fmt.Sprintf("other %d", id), 7))
	}
	manyRoots = append(manyRoots, folderJSON(150, "Case 7", 7))

	tests := []struct {
		name        string
		roots       []string
		subfolders  []string
		parentID    int
		folderName  string
		wantID      int
		wantCreated []string
		wantMatches []int
	}{
		{name: "existing root folder", roots: []string{folderJSON(1, "Case 7", 7)}, folderName: "Case 7", wantID: 1},
		{name: "existing root folder on a later page", roots: manyRoots, folderName: "Case 7", wantID: 150},
		{
			name:        "root folder owned by someone else is ignored",
			roots:       []string{folderJSON(1, "Case 7", 8)},
			folderName:  "Case 7",
			wantID:      900,
			wantCreated: []string{"root/Case 7"},
		},
		{
			name:        "names are case-sensitive",
			roots:       []string{folderJSON(1, "case 7", 7)},
			folderName:  "Case 7",
			wantID:      900,
			wantCreated: []string{"root/Case 7"},
		},
		{name: "existing subfolder", subfolders: []string{folderJSON(51, "logs", 8)}, parentID: 50, folderName: "logs", wantID: 51},
		{
			name:        "missing subfolder is created",
			subfolders:  []string{folderJSON(51, "other", 7)},
			parentID:    50,
			folderName:  "logs",
			wantID:      900,
			wantCreated: []string{"50/logs"},
		},
		{
			name:        "several matches",
			roots:       []string{folderJSON(1, "Case 7", 7), folderJSON(2, "Case 7", 7)},
			folderName:  "Case 7",
			wantMatches: []int{1, 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var created []string
			client := newFindOrCreateServer(t, tt.roots, tt.subfolders, &created)

			folder, wasCreated, err := FindOrCreateFolder(context.Background(), client, tt.parentID, tt.folderName)
			if tt.wantMatches != nil {
				var multiple *MultipleFoldersError
				if !errors.As(err, &multiple) {
					t.Fatalf("expected MultipleFoldersError, got %v", err)
				}
				var ids []int
				for _, f := range multiple.Matches {
					ids = append(ids, f.ID)
				}
				if fmt.Sprint(ids) != fmt.Sprint(tt.wantMatches) {
					t.Errorf("matches %v, want %v", ids, tt.wantMatches)
				}
				if len(created) > 0 {
					t.Errorf("nothing should be created, got %v", created)
				}
				return
			}
			if err != nil {
				t.Fatalf("FindOrCreateFolder failed: %v", err)
			}
			if folder.ID != tt.wantID {
				t.Errorf("got folder %d, want %d", folder.ID, tt.wantID)
			}
			if wasCreated != (len(tt.wantCreated) > 0) {
				t.Errorf("created = %v, want %v", wasCreated, len(tt.wantCreated) > 0)
			}
			if fmt.Sprint(created) != fmt.Sprint(tt.wantCreated) {
				t.Errorf("created folders %v, want %v", created, tt.wantCreated)
			}
		})
	}
}

func TestLatestFolder(t *testing.T) {
	at := func(day int) time.Time { return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name    string
		folders []openrelik.Folder
		want    int
	}{
		{"newest update wins over higher ID", []openrelik.Folder{{ID: 1, UpdatedAt: at(3)}, {ID: 2, UpdatedAt: at(2)}}, 1},
		{"newest update anywhere in the list", []openrelik.Folder{{ID: 1, UpdatedAt: at(1)}, {ID: 2, UpdatedAt: at(1)}, {ID: 3, UpdatedAt: at(5)}}, 3},
		{"tie goes to the higher ID", []openrelik.Folder{{ID: 9, UpdatedAt: at(1)}, {ID: 4, UpdatedAt: at(1)}}, 9},
	}
	for _, tt := range tests {
		if got := LatestFolder(tt.folders).ID; got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

// newRootFolderServer serves total root folders, with IDs from 1, in pages of
// at most maxPageSize. Folder dupID, if set, is also served at the start of
// the next page, as when a folder is created while listing. Requests for
// failPage get a server error.
func newRootFolderServer(t *testing.T, total, maxPageSize, dupID, failPage int) *openrelik.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		size = min(size, maxPageSize)
		if page == failPage {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var items []string
		if dupID > 0 && dupID == (page-1)*size {
			items = append(items, folderJSON(dupID, fmt.Sprintf("Root %d", dupID), 7))
		}
		for id := (page-1)*size + 1; id <= min(page*size, total); id++ {
			items = append(items, folderJSON(id, fmt.Sprintf("Root %d", id), 7))
		}
		fmt.Fprintf(w, `{"folders": [%s], "page": %d, "page_size": %d, "total_count": %d}`,
			strings.Join(items, ","), page, size, total)
	}))
	t.Cleanup(server.Close)
	client, _ := openrelik.NewClient(server.URL, "test-key")
	return client
}

func TestListAllRootFolders(t *testing.T) {
	tests := []struct {
		name         string
		total        int
		maxPageSize  int
		dupID        int
		wantProgress int
	}{
		{name: "empty", total: 0, maxPageSize: 100},
		{name: "one page", total: 60, maxPageSize: 100},
		{name: "several pages", total: 950, maxPageSize: 100, wantProgress: 10},
		{name: "server caps the page size", total: 120, maxPageSize: 25, wantProgress: 5},
		{name: "folder shifted to the next page", total: 250, maxPageSize: 100, dupID: 100, wantProgress: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newRootFolderServer(t, tt.total, tt.maxPageSize, tt.dupID, 0)
			var progress []int
			folders, err := ListAllRootFolders(context.Background(), client, func(fetched, total int) {
				if total != tt.total {
					t.Errorf("progress total = %d, want %d", total, tt.total)
				}
				progress = append(progress, fetched)
			})
			if err != nil {
				t.Fatalf("ListAllRootFolders() failed: %v", err)
			}
			if len(folders) != tt.total {
				t.Fatalf("got %d folders, want %d", len(folders), tt.total)
			}
			// The server's order is kept.
			for i, f := range folders {
				if f.ID != i+1 {
					t.Fatalf("folder %d has ID %d, want %d", i, f.ID, i+1)
				}
			}
			if len(progress) != tt.wantProgress {
				t.Errorf("progress called %d times, want %d: %v", len(progress), tt.wantProgress, progress)
			}
			for i := 1; i < len(progress); i++ {
				if progress[i] <= progress[i-1] {
					t.Errorf("progress went from %d to %d", progress[i-1], progress[i])
				}
			}
		})
	}
}

func TestListAllRootFoldersError(t *testing.T) {
	client := newRootFolderServer(t, 500, 100, 0, 3)
	folders, err := ListAllRootFolders(context.Background(), client, nil)
	if err == nil {
		t.Fatalf("got %d folders and no error, want an error for the failed page", len(folders))
	}
}

// newCursorRootFolderServer serves total root folders, with IDs from 1, in
// the order asked for (only ID order is mocked: asc or desc), echoing sort
// as echoSort. The cursor is the index of the next folder. Each request's
// page_size is recorded in sizes.
func newCursorRootFolderServer(t *testing.T, total int, echoSort string, sizes *[]int) *openrelik.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		size, _ := strconv.Atoi(q.Get("page_size"))
		*sizes = append(*sizes, size)
		start, _ := strconv.Atoi(q.Get("after"))
		end := min(start+size, total)
		var items []string
		for i := start; i < end; i++ {
			id := i + 1
			if q.Get("order") == "desc" {
				id = total - i
			}
			items = append(items, folderJSON(id, fmt.Sprintf("Root %d", id), 7))
		}
		next := "null"
		if end < total {
			next = strconv.Quote(strconv.Itoa(end))
		}
		fmt.Fprintf(w, `{"folders": [%s], "page": 1, "page_size": %d, "total_count": %d, "sort": %q, "order": %q, "next_cursor": %s}`,
			strings.Join(items, ","), size, total, echoSort, q.Get("order"), next)
	}))
	t.Cleanup(server.Close)
	client, _ := openrelik.NewClient(server.URL, "test-key")
	return client
}

func TestStreamRootFolders(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		limit     int
		wantIDs   int
		wantSizes []int
	}{
		{name: "all", total: 2500, wantIDs: 2500, wantSizes: []int{100, 1000, 1000, 1000}},
		{name: "limit within the first page", total: 2500, limit: 10, wantIDs: 10, wantSizes: []int{10}},
		{name: "limit of a full batch", total: 2500, limit: 1000, wantIDs: 1000, wantSizes: []int{1000}},
		{name: "limit above total", total: 30, limit: 100, wantIDs: 30, wantSizes: []int{100}},
		{name: "empty", total: 0, wantIDs: 0, wantSizes: []int{100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sizes []int
			client := newCursorRootFolderServer(t, tt.total, "updated_at", &sizes)
			var ids []int
			total, ok, err := StreamRootFolders(context.Background(), client, "updated_at", "desc", tt.limit, func(page []openrelik.Folder) error {
				if len(page) == 0 {
					t.Error("each called with an empty page")
				}
				for _, f := range page {
					ids = append(ids, f.ID)
				}
				return nil
			})
			if err != nil || !ok {
				t.Fatalf("StreamRootFolders() = %v, %v", ok, err)
			}
			if total != tt.total {
				t.Errorf("total = %d, want %d", total, tt.total)
			}
			if len(ids) != tt.wantIDs {
				t.Fatalf("got %d folders, want %d", len(ids), tt.wantIDs)
			}
			for i, id := range ids {
				if id != tt.total-i {
					t.Fatalf("folder %d has ID %d, want %d", i, id, tt.total-i)
				}
			}
			if fmt.Sprint(sizes) != fmt.Sprint(tt.wantSizes) {
				t.Errorf("page sizes requested = %v, want %v", sizes, tt.wantSizes)
			}
		})
	}
}

func TestStreamRootFoldersUnsupported(t *testing.T) {
	var sizes []int
	for name, client := range map[string]*openrelik.Client{
		"older server":       newRootFolderServer(t, 250, 100, 0, 0),
		"other sort applied": newCursorRootFolderServer(t, 250, "created_at", &sizes),
	} {
		t.Run(name, func(t *testing.T) {
			_, ok, err := StreamRootFolders(context.Background(), client, "updated_at", "desc", 0, func([]openrelik.Folder) error {
				t.Error("each must not be called when the server didn't sort")
				return nil
			})
			if err != nil || ok {
				t.Errorf("StreamRootFolders() = %v, %v; want false, nil", ok, err)
			}
		})
	}
}

func TestStreamRootFoldersStopsOnError(t *testing.T) {
	var sizes []int
	client := newCursorRootFolderServer(t, 2500, "updated_at", &sizes)
	stop := errors.New("stop")
	_, _, err := StreamRootFolders(context.Background(), client, "updated_at", "asc", 0, func([]openrelik.Folder) error { return stop })
	if !errors.Is(err, stop) {
		t.Errorf("got error %v, want %v", err, stop)
	}
	if len(sizes) != 1 {
		t.Errorf("got %d requests after each failed, want 1", len(sizes))
	}
}

func TestListAllRootFoldersFollowsCursors(t *testing.T) {
	var sizes []int
	client := newCursorRootFolderServer(t, 2500, "created_at", &sizes)
	var progress []int
	folders, err := ListAllRootFolders(context.Background(), client, func(fetched, total int) { progress = append(progress, fetched) })
	if err != nil {
		t.Fatalf("ListAllRootFolders() failed: %v", err)
	}
	if len(folders) != 2500 || folders[0].ID != 2500 {
		t.Errorf("got %d folders starting at %d, want 2500 newest first", len(folders), folders[0].ID)
	}
	if fmt.Sprint(sizes) != "[100 1000 1000 1000]" {
		t.Errorf("page sizes requested = %v", sizes)
	}
	if fmt.Sprint(progress) != "[100 1100 2100 2500]" {
		t.Errorf("progress = %v", progress)
	}
}

func TestListAllRootFoldersUnconfirmedOrder(t *testing.T) {
	// The server echoes another order than the one asked for, so its cursors
	// aren't trusted and the pages are fetched by number instead.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("after") != "" {
			t.Errorf("cursor followed: %s", r.URL)
		}
		page, _ := strconv.Atoi(q.Get("page"))
		var items []string
		for id := (page-1)*100 + 1; id <= min(page*100, 250); id++ {
			items = append(items, folderJSON(id, fmt.Sprintf("Root %d", id), 7))
		}
		fmt.Fprintf(w, `{"folders": [%s], "page": %d, "page_size": 100, "total_count": 250, "sort": "created_at", "order": "asc", "next_cursor": "x"}`,
			strings.Join(items, ","), page)
	}))
	t.Cleanup(server.Close)
	client, _ := openrelik.NewClient(server.URL, "test-key")
	folders, err := ListAllRootFolders(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("ListAllRootFolders() failed: %v", err)
	}
	if len(folders) != 250 {
		t.Errorf("got %d folders, want 250", len(folders))
	}
}

func TestListAllRootFoldersProgressPastFirstTotal(t *testing.T) {
	// The first page counts 150 root folders, but 200 are served, as when
	// older folders are shared while listing. Cursor pages have no total.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		size, _ := strconv.Atoi(q.Get("page_size"))
		start, _ := strconv.Atoi(q.Get("after"))
		end := min(start+size, 200)
		var items []string
		for i := start; i < end; i++ {
			items = append(items, folderJSON(200-i, fmt.Sprintf("Root %d", 200-i), 7))
		}
		total, next := "null", "null"
		if q.Get("after") == "" {
			total = "150"
		}
		if end < 200 {
			next = strconv.Quote(strconv.Itoa(end))
		}
		fmt.Fprintf(w, `{"folders": [%s], "page": null, "page_size": %d, "total_count": %s, "sort": "created_at", "order": "desc", "next_cursor": %s}`,
			strings.Join(items, ","), size, total, next)
	}))
	t.Cleanup(server.Close)
	client, _ := openrelik.NewClient(server.URL, "test-key")
	var progress []string
	folders, err := ListAllRootFolders(context.Background(), client, func(fetched, total int) {
		if fetched > total {
			t.Errorf("progress %d of %d", fetched, total)
		}
		progress = append(progress, fmt.Sprintf("%d/%d", fetched, total))
	})
	if err != nil {
		t.Fatalf("ListAllRootFolders() failed: %v", err)
	}
	if len(folders) != 200 {
		t.Errorf("got %d folders, want 200", len(folders))
	}
	if fmt.Sprint(progress) != "[100/150 200/200]" {
		t.Errorf("progress = %v", progress)
	}
}

func TestStreamRootFoldersLimitAboveBatch(t *testing.T) {
	var sizes []int
	client := newCursorRootFolderServer(t, 2500, "updated_at", &sizes)
	_, _, err := StreamRootFolders(context.Background(), client, "updated_at", "desc", RootFolderBatchSize+1, func([]openrelik.Folder) error { return nil })
	if err == nil || len(sizes) != 0 {
		t.Errorf("got error %v after %d requests, want an error before any request", err, len(sizes))
	}
}

func TestStreamRootFoldersSkipsRepeatedFolders(t *testing.T) {
	// Folder 50 is updated while listing, so it moves to the end of the
	// updated_at ascending order and comes back on the second page.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ids []int
		next := `"2"`
		if r.URL.Query().Get("after") == "" {
			for id := 1; id <= 100; id++ {
				ids = append(ids, id)
			}
		} else {
			ids, next = []int{101, 102, 50}, "null"
		}
		var items []string
		for _, id := range ids {
			items = append(items, folderJSON(id, fmt.Sprintf("Root %d", id), 7))
		}
		fmt.Fprintf(w, `{"folders": [%s], "page": 1, "page_size": 100, "total_count": 102, "sort": "updated_at", "order": "asc", "next_cursor": %s}`,
			strings.Join(items, ","), next)
	}))
	defer server.Close()
	client, _ := openrelik.NewClient(server.URL, "test-key")

	seen := make(map[int]int)
	_, _, err := StreamRootFolders(context.Background(), client, "updated_at", "asc", 0, func(page []openrelik.Folder) error {
		for _, f := range page {
			seen[f.ID]++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamRootFolders() failed: %v", err)
	}
	if len(seen) != 102 || seen[50] != 1 {
		t.Errorf("got %d folders, folder 50 %d times; want 102, once", len(seen), seen[50])
	}
}

// newChangingRootFolderServer serves root folders newest first by page
// number, like an older server, with folders 1 to total. After the first
// request, change folders are created (IDs from total+1) if change > 0, which
// shifts the later pages, or the oldest -change folders are deleted if
// change < 0. Pages hold at most maxPageSize folders, and page_size is
// reported as reportedSize. requests counts the requests served.
func newChangingRootFolderServer(t *testing.T, total, change, maxPageSize int, reportedSize func(asked int) int, requests *int) *openrelik.Client {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*requests++
		newest, oldest := total, 1
		if *requests > 1 {
			newest, oldest = total+max(change, 0), 1+max(-change, 0)
		}
		mu.Unlock()
		n := newest - oldest + 1
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		asked := size
		size = min(size, maxPageSize)
		var items []string
		for i := (page - 1) * size; i < min(page*size, n); i++ {
			items = append(items, folderJSON(newest-i, fmt.Sprintf("Root %d", newest-i), 7))
		}
		fmt.Fprintf(w, `{"folders": [%s], "page": %d, "page_size": %d, "total_count": %d}`,
			strings.Join(items, ","), page, reportedSize(asked), n)
	}))
	t.Cleanup(server.Close)
	client, _ := openrelik.NewClient(server.URL, "test-key")
	return client
}

func TestListAllRootFoldersByPageWhileFoldersChange(t *testing.T) {
	asAsked := func(asked int) int { return asked }
	tests := []struct {
		name         string
		total        int
		change       int
		maxPageSize  int
		reportedSize func(int) int
		wantRequests int
	}{
		// Folder 1 is pushed to page 3, which is short: no request past it.
		{name: "folder created while listing", total: 200, change: 1, maxPageSize: 100, reportedSize: asAsked, wantRequests: 3},
		{name: "unreported page size cap", total: 200, change: 1, maxPageSize: 50, reportedSize: func(int) int { return 0 }, wantRequests: 5},
		{name: "page size cap reported as asked", total: 200, change: 1, maxPageSize: 50, reportedSize: asAsked, wantRequests: 5},
		// Folders 1 and 2 are pushed to page 3 by the shift.
		{name: "two folders created", total: 199, change: 2, maxPageSize: 100, reportedSize: asAsked, wantRequests: 3},
		// The total stays at 200 from page 1, but page 2 is short.
		{name: "folder deleted while listing", total: 200, change: -1, maxPageSize: 100, reportedSize: asAsked, wantRequests: 2},
		// The only folder on page 3 is deleted, so page 3 comes back empty.
		{name: "last page emptied while listing", total: 201, change: -1, maxPageSize: 100, reportedSize: asAsked, wantRequests: 3},
		{name: "unchanged", total: 200, maxPageSize: 100, reportedSize: asAsked, wantRequests: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			client := newChangingRootFolderServer(t, tt.total, tt.change, tt.maxPageSize, tt.reportedSize, &requests)
			var progress []int
			folders, err := ListAllRootFolders(context.Background(), client, func(fetched, total int) {
				progress = append(progress, fetched)
			})
			if err != nil {
				t.Fatalf("ListAllRootFolders() failed: %v", err)
			}
			seen := make(map[int]bool)
			for _, f := range folders {
				if seen[f.ID] {
					t.Errorf("folder %d listed twice", f.ID)
				}
				seen[f.ID] = true
			}
			// Every folder that existed when the listing started, and still
			// exists, is there.
			for id := 1 + max(-tt.change, 0); id <= tt.total; id++ {
				if !seen[id] {
					t.Errorf("folder %d is missing", id)
				}
			}
			if requests != tt.wantRequests {
				t.Errorf("got %d requests, want %d", requests, tt.wantRequests)
			}
			for i := 1; i < len(progress); i++ {
				if progress[i] <= progress[i-1] {
					t.Errorf("progress went from %d to %d: %v", progress[i-1], progress[i], progress)
				}
			}
		})
	}
}
