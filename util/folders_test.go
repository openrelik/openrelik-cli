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
	"testing"

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
