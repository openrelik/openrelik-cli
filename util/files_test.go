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
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

		mux.HandleFunc("/api/v1/users/me/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id": 7, "username": "me"}`)
		})
		mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"folders": [{"id": 2, "display_name": "Custom Folder", "user": {"id": 7}}]}`)
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

// resultsServer is a mock API holding workflow 100, whose tasks produce the
// given output files. Every file's content is "hello" and its SHA256 on the
// server is sha256 ("" for none).
func resultsServer(t *testing.T, tasks string, sha256 string) *openrelik.Client {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/api/v1/workflows/100", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id": 100, "tasks": %s}`, tasks)
	})
	mux.HandleFunc("/api/v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id": %s, "display_name": "x", "filesize": 5, "hash_sha256": %q}`, r.PathValue("id"), sha256)
	})
	mux.HandleFunc("/api/v1/files/{id}/download/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	})
	client, _ := openrelik.NewClient(server.URL, "test-key")
	return client
}

// helloSHA256 is the SHA256 of "hello".
const helloSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

func TestDownloadResults(t *testing.T) {
	ctx := context.Background()
	oneTask := `[{"uuid": "task1", "id": 1, "display_name": "Task 1",
		"output_files": [{"id": 10, "display_name": "output.txt", "filesize": 5}]}]`

	t.Run("Download all", func(t *testing.T) {
		tmpDir := t.TempDir()
		client := resultsServer(t, oneTask, helloSHA256)
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
		client := resultsServer(t, oneTask, helloSHA256)
		total, _, err := DownloadResults(ctx, client, 100, "none", nil, t.TempDir(), false, "text", false)
		if err != nil {
			t.Fatalf("DownloadResults failed: %v", err)
		}
		if total != 0 {
			t.Errorf("Expected total 0, got %d", total)
		}
	})

	tests := []struct {
		name        string
		tasks       string
		taskFolders bool
		sha256      string
		existing    string // file in the output dir before the download
		wantFiles   []string
		wantErr     string
	}{
		{
			name: "hostile names stay in the output dir",
			tasks: `[{"uuid": "t1", "id": 1, "display_name": "../../Task",
				"output_files": [{"id": 10, "display_name": "../../outside.txt"}]}]`,
			taskFolders: true,
			sha256:      helloSHA256,
			wantFiles:   []string{"Task_1/outside.txt"},
		},
		{
			name: "unusable name",
			tasks: `[{"uuid": "t1", "id": 1, "display_name": "Task",
				"output_files": [{"id": 10, "display_name": ".."}]}]`,
			wantErr: `name ".." can't be used as a local file name`,
		},
		{
			name: "existing file is not replaced",
			tasks: `[{"uuid": "t1", "id": 1, "display_name": "Task",
				"output_files": [{"id": 10, "display_name": "output.txt"}]}]`,
			existing: "output.txt",
			wantErr:  `already exists; move it away or choose another --output-dir`,
		},
		{
			name: "tasks with the same output name",
			tasks: `[{"uuid": "t1", "id": 1, "display_name": "A", "output_files": [{"id": 10, "display_name": "out.txt"}]},
				{"uuid": "t2", "id": 2, "display_name": "B", "output_files": [{"id": 11, "display_name": "out.txt"}]}]`,
			wantErr: `tasks "A" and "B" both produce`,
		},
		{
			name: "tasks with the same output name in task folders",
			tasks: `[{"uuid": "t1", "id": 1, "display_name": "A", "output_files": [{"id": 10, "display_name": "out.txt"}]},
				{"uuid": "t2", "id": 2, "display_name": "B", "output_files": [{"id": 11, "display_name": "out.txt"}]}]`,
			taskFolders: true,
			wantFiles:   []string{"A_1/out.txt", "B_2/out.txt"},
		},
		{
			name: "hash mismatch",
			tasks: `[{"uuid": "t1", "id": 1, "display_name": "Task",
				"output_files": [{"id": 10, "display_name": "output.txt"}]}]`,
			sha256:  "0000",
			wantErr: "was not saved: its SHA256 on the server is 0000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			outDir := filepath.Join(base, "a", "b")
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.existing != "" {
				os.WriteFile(filepath.Join(outDir, tt.existing), []byte("original"), 0o644)
			}
			client := resultsServer(t, tt.tasks, tt.sha256)
			_, _, err := DownloadResults(ctx, client, 100, "all", nil, outDir, tt.taskFolders, "text", false)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("DownloadResults failed: %v", err)
			}
			for _, f := range tt.wantFiles {
				if got, _ := os.ReadFile(filepath.Join(outDir, f)); string(got) != "hello" {
					t.Errorf("expected %s to hold the download, got %q", f, got)
				}
			}
			if tt.existing != "" {
				if got, _ := os.ReadFile(filepath.Join(outDir, tt.existing)); string(got) != "original" {
					t.Errorf("%s was replaced: %q", tt.existing, got)
				}
			}
			// Nothing is written outside the output dir, and no partial
			// files are left behind.
			filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				rel, _ := filepath.Rel(outDir, path)
				if !d.IsDir() && (strings.HasPrefix(rel, "..") || strings.HasSuffix(path, ".partial")) {
					t.Errorf("unexpected file %s", path)
				}
				return nil
			})
		})
	}
}

func TestSaveFile(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		file         openrelik.File
		existing     bool
		overwrite    bool
		wantVerified bool
		wantErr      string
	}{
		{name: "verified", file: openrelik.File{ID: 10, Filesize: 5, HashSHA256: helloSHA256}, wantVerified: true},
		{name: "hash in upper case", file: openrelik.File{ID: 10, HashSHA256: strings.ToUpper(helloSHA256)}, wantVerified: true},
		{name: "no hash on server", file: openrelik.File{ID: 10, Filesize: 5}},
		{name: "hash mismatch", file: openrelik.File{ID: 10, HashSHA256: "0000"}, wantErr: "file 10 was not saved: its SHA256 on the server is 0000"},
		{name: "size mismatch without hash", file: openrelik.File{ID: 10, Filesize: 9}, wantErr: "it is 9 bytes on the server, but 5 bytes were downloaded"},
		{name: "existing file is kept", file: openrelik.File{ID: 10, HashSHA256: helloSHA256}, existing: true, wantErr: "already exists"},
		{name: "existing file is overwritten", file: openrelik.File{ID: 10, HashSHA256: helloSHA256}, existing: true, overwrite: true, wantVerified: true},
		{name: "mismatch keeps existing file", file: openrelik.File{ID: 10, HashSHA256: "0000"}, existing: true, overwrite: true, wantErr: "was not saved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := resultsServer(t, "[]", "")
			dir := t.TempDir()
			dest := filepath.Join(dir, "out.txt")
			if tt.existing {
				os.WriteFile(dest, []byte("original"), 0o644)
			}
			saved, err := SaveFile(ctx, client, &tt.file, dest, tt.overwrite, nil)

			entries, _ := os.ReadDir(dir)
			if len(entries) > 1 || (len(entries) == 1 && entries[0].Name() != "out.txt") {
				t.Errorf("unexpected files left in %s: %v", dir, entries)
			}
			got, _ := os.ReadFile(dest)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				if tt.existing && string(got) != "original" {
					t.Errorf("existing file was changed: %q", got)
				}
				if !tt.existing && len(entries) != 0 {
					t.Errorf("a file was saved despite the error")
				}
				return
			}
			if err != nil {
				t.Fatalf("SaveFile failed: %v", err)
			}
			if string(got) != "hello" {
				t.Errorf("saved %q, want %q", got, "hello")
			}
			if saved.SHA256 != helloSHA256 || saved.Verified != tt.wantVerified || saved.Size != 5 {
				t.Errorf("got %+v, want SHA256 %s, size 5, verified %v", saved, helloSHA256, tt.wantVerified)
			}
		})
	}
}

func TestVerifyUpload(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name         string
		fileHash     string // hash in the upload response
		serverHash   string // hash from file info
		wantVerified bool
		wantErr      string
	}{
		{name: "hash in response", fileHash: helloSHA256, wantVerified: true},
		{name: "hash from file info", serverHash: helloSHA256, wantVerified: true},
		{name: "no hash yet"},
		{name: "mismatch", fileHash: "0000", wantErr: "file 10 on the server doesn't match what was uploaded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := resultsServer(t, "[]", tt.serverHash)
			verified, err := VerifyUpload(ctx, client, &openrelik.File{ID: 10, HashSHA256: tt.fileHash}, helloSHA256)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || verified != tt.wantVerified {
				t.Errorf("VerifyUpload() = %v, %v; want %v", verified, err, tt.wantVerified)
			}
		})
	}
}

func TestLocalFileName(t *testing.T) {
	tests := []struct {
		displayName string
		want        string // empty means an error
	}{
		{"report.pdf", "report.pdf"},
		{"../../.bashrc", ".bashrc"},
		{"/usr/bin/ls", "ls"},
		{"evidence/logs/syslog", "syslog"},
		{"..", ""},
		{".", ""},
		{"/", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got, err := LocalFileName(tt.displayName)
		if tt.want == "" {
			if err == nil {
				t.Errorf("LocalFileName(%q) = %q, want an error", tt.displayName, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("LocalFileName(%q) = %q, %v; want %q", tt.displayName, got, err, tt.want)
		}
	}
}
