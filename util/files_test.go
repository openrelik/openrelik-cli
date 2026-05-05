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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openrelik/openrelik-go-client"
)

func TestResolveInputs(t *testing.T) {
	// Temp file for local upload test
	tmpDir, err := os.MkdirTemp("", "openrelik-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	t.Run("File IDs only", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")
		ctx := context.Background()

		ids, total, err := ResolveInputs(ctx, client, []string{"123", "456"}, false, 0, "")
		if err != nil {
			t.Fatalf("ResolveInputs failed: %v", err)
		}
		if len(ids) != 2 || ids[0] != 123 || ids[1] != 456 {
			t.Errorf("Unexpected IDs: %v", ids)
		}
		if total != 0 {
			t.Errorf("Expected total 0, got %d", total)
		}
	})

	t.Run("Local file with explicit folder ID", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")
		ctx := context.Background()

		mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 789, "display_name": "test.txt"}`)
			}
		})

		ids, total, err := ResolveInputs(ctx, client, []string{tmpFile}, false, 123, "")
		if err != nil {
			t.Fatalf("ResolveInputs failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != 789 {
			t.Errorf("Unexpected IDs: %v", ids)
		}
		if total != 11 {
			t.Errorf("Expected total 11, got %d", total)
		}
	})

	t.Run("Local file with explicit folder name", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")
		ctx := context.Background()

		mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"folders": [{"id": 2, "display_name": "Custom Folder"}]}`)
		})
		mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 789, "display_name": "test.txt"}`)
			}
		})

		ids, total, err := ResolveInputs(ctx, client, []string{tmpFile}, false, 0, "Custom Folder")
		if err != nil {
			t.Fatalf("ResolveInputs failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != 789 {
			t.Errorf("Unexpected IDs: %v", ids)
		}
		if total != 11 {
			t.Errorf("Expected total 11, got %d", total)
		}
	})

	t.Run("Local file with per-user default folder", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")
		ctx := context.Background()

		mux.HandleFunc("/api/v1/users/me/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"username": "testuser"}`)
		})
		mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"folders": [{"id": 3, "display_name": "CLI Uploads (testuser)"}]}`)
		})
		mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 789, "display_name": "test.txt"}`)
			}
		})

		ids, total, err := ResolveInputs(ctx, client, []string{tmpFile}, false, 0, "")
		if err != nil {
			t.Fatalf("ResolveInputs failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != 789 {
			t.Errorf("Unexpected IDs: %v", ids)
		}
		if total != 11 {
			t.Errorf("Expected total 11, got %d", total)
		}
	})

	t.Run("Invalid Input", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")
		ctx := context.Background()

		_, _, err := ResolveInputs(ctx, client, []string{"not-a-file-and-not-an-id"}, false, 0, "")
		if err == nil {
			t.Fatal("Expected error for invalid input, got nil")
		}
	})
}

func TestDownloadResults(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	client, _ := openrelik.NewClient(server.URL, "test-key")
	ctx := context.Background()

	tmpDir, _ := os.MkdirTemp("", "openrelik-dl-test")
	defer os.RemoveAll(tmpDir)

	mux.HandleFunc("/api/v1/workflows/100", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": 100,
			"tasks": [
				{
					"uuid": "task1",
					"id": 1,
					"display_name": "Task 1",
					"output_files": [
						{"id": 10, "display_name": "output.txt", "filesize": 5}
					]
				}
			]
		}`)
	})

	mux.HandleFunc("/api/v1/files/10/download/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	})

	t.Run("Download all", func(t *testing.T) {
		total, _, err := DownloadResults(ctx, client, 100, "all", nil, tmpDir, false, "text", false)
		if err != nil {
			t.Fatalf("DownloadResults failed: %v", err)
		}
		if total != 5 {
			t.Errorf("Expected total 5, got %d", total)
		}

		content, err := os.ReadFile(filepath.Join(tmpDir, "output.txt"))
		if err != nil {
			t.Fatalf("Failed to read downloaded file: %v", err)
		}
		if string(content) != "hello" {
			t.Errorf("Unexpected content: %s", string(content))
		}
	})

	t.Run("Download none", func(t *testing.T) {
		total, _, err := DownloadResults(ctx, client, 100, "none", nil, tmpDir, false, "text", false)
		if err != nil {
			t.Fatalf("DownloadResults failed: %v", err)
		}
		if total != 0 {
			t.Errorf("Expected total 0, got %d", total)
		}
	})
}
