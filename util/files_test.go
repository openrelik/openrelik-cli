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
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

		ids, total, err := ResolveInputs(ctx, client, noServerPaths, []string{"123", "456"}, false, 0, "")
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

		mux.HandleFunc("/api/v1/folders/123/files/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		})
		mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 789, "display_name": "test.txt", "hash_sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"}`)
			}
		})

		ids, total, err := ResolveInputs(ctx, client, noServerPaths, []string{tmpFile}, false, 123, "")
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
		mux.HandleFunc("/api/v1/folders/2/files/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		})
		mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 789, "display_name": "test.txt", "hash_sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"}`)
			}
		})

		ids, total, err := ResolveInputs(ctx, client, noServerPaths, []string{tmpFile}, false, 0, "Custom Folder")
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
		mux.HandleFunc("/api/v1/folders/3/files/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		})
		mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 789, "display_name": "test.txt", "hash_sha256": "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"}`)
			}
		})

		ids, total, err := ResolveInputs(ctx, client, noServerPaths, []string{tmpFile}, false, 0, "")
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

		_, _, err := ResolveInputs(ctx, client, noServerPaths, []string{"not-a-file-and-not-an-id"}, false, 0, "")
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
		saved, _, err := DownloadResults(ctx, client, 100, "all", nil, tmpDir, false, false)
		if err != nil {
			t.Fatalf("DownloadResults failed: %v", err)
		}
		want := SavedFile{FileID: 10, Path: filepath.Join(tmpDir, "output.txt"), Size: 5, SHA256: helloSHA256, Verified: true}
		if len(saved) != 1 || *saved[0] != want {
			t.Errorf("saved %+v, want [%+v]", saved, want)
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
		saved, _, err := DownloadResults(ctx, client, 100, "none", nil, t.TempDir(), false, false)
		if err != nil {
			t.Fatalf("DownloadResults failed: %v", err)
		}
		if len(saved) != 0 {
			t.Errorf("Expected nothing saved, got %+v", saved)
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
			wantErr:  `already exists; move it away or download to another directory`,
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
			_, _, err := DownloadResults(ctx, client, 100, "all", nil, outDir, tt.taskFolders, false)
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

func TestClassifyInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, f := range []string{"4121", "evidence.bin"} {
		os.WriteFile(f, []byte("x"), 0o644)
	}
	os.Mkdir("2024", 0o755)
	os.Mkdir("logs", 0o755)

	tests := []struct {
		arg     string
		want    input
		wantErr string
	}{
		{arg: "123", want: input{kind: inputID, id: 123}},
		{arg: "evidence.bin", want: input{kind: inputLocal, path: "evidence.bin"}},
		{arg: "./4121", want: input{kind: inputLocal, path: "./4121"}},
		{arg: "4121", wantErr: "\"4121\" is both a file ID and a local file\nUse ./4121 to upload the local file, or run from a directory without it to use file 4121."},
		{arg: "2024", want: input{kind: inputID, id: 2024}}, // a directory can't be an input, so this is the ID
		{arg: "0", wantErr: "invalid file ID: 0"},
		{arg: "Case A/disk.E01", want: input{kind: inputServer, path: "Case A/disk.E01"}},
		{arg: "Case A/", wantErr: `"Case A/" is neither a file ID, a local file nor a server path (FOLDER/NAME)`},
		{arg: "missing.bin", wantErr: `"missing.bin" is neither a file ID, a local file nor a server path (FOLDER/NAME)`},
		{arg: "logs", wantErr: `"logs" is a directory`},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := classifyInput(tt.arg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("classifyInput(%q) = %+v, %v; want %+v", tt.arg, got, err, tt.want)
			}
		})
	}
}

// noServerPaths is a ServerPathResolver for a server without files.
func noServerPaths(ctx context.Context, path string) (int, error) {
	return 0, &NotOnServerError{Err: fmt.Errorf("no file at %s", path)}
}

func TestResolveInputsServerPaths(t *testing.T) {
	t.Chdir(t.TempDir())
	os.Mkdir("Case A", 0o755)
	os.WriteFile("Case A/local.bin", []byte("x"), 0o644)
	os.WriteFile("Case A/both.bin", []byte("x"), 0o644)
	resolve := func(ctx context.Context, path string) (int, error) {
		switch strings.TrimPrefix(path, "/") {
		case "Case A/disk.E01":
			return 812, nil
		case "Case A/both.bin":
			return 813, nil
		case "Case A/down.bin":
			return 0, errors.New("could not reach the server")
		}
		return 0, &NotOnServerError{Err: errors.New(`no file named "x"`)}
	}
	ctx := context.Background()

	tests := []struct {
		name      string
		args      []string
		want      []int
		wantErr   string
		wantInput bool // a mistake in the command line
	}{
		{name: "order is kept", args: []string{"5", "Case A/disk.E01", "/Case A/disk.E01", "3"}, want: []int{5, 812, 812, 3}},
		{name: "not on the server", args: []string{"Case A/typo.E01"}, wantErr: `"Case A/typo.E01" is neither a local file nor a file on the server: no file named "x"`},
		{name: "server error", args: []string{"Case A/down.bin"}, wantErr: "could not reach the server"},
		{name: "both local and on the server", args: []string{"Case A/both.bin"}, wantErr: "\"Case A/both.bin\" is both a local file and file 813 on the server\nUse ./Case A/both.bin to upload the local file, or 813 to use the server file.", wantInput: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No server: these inputs must not need one.
			got, _, err := ResolveInputs(ctx, nil, resolve, tt.args, false, 0, "")
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				if isInput := errors.As(err, new(*InputError)); isInput != tt.wantInput {
					t.Errorf("error is an InputError: %v, want %v", isInput, tt.wantInput)
				}
				return
			}
			if err != nil || !slices.Equal(got, tt.want) {
				t.Errorf("ResolveInputs(%q) = %v, %v; want %v", tt.args, got, err, tt.want)
			}
		})
	}

	// ./PATH is always local, so the server isn't asked about it.
	t.Run("./ skips the server", func(t *testing.T) {
		asked := false
		resolve := func(ctx context.Context, path string) (int, error) {
			asked = true
			return 1, nil
		}
		// The upload fails without a server; only the check matters here.
		ResolveInputs(ctx, uploadServer(t, `[]`, nil), resolve, []string{"./Case A/local.bin"}, false, 40, "")
		if asked {
			t.Error("the server was asked about ./Case A/local.bin")
		}
	})
}

// uploadServer is a mock API whose folder 40 holds the given files. Each
// file's SHA256 is in hashes. Uploads get ID 900 and are counted in
// *uploads, if uploads isn't nil.
func uploadServer(t *testing.T, files string, hashes map[int]string, uploads ...*int) *openrelik.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/folders/40/files/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, files)
	})
	mux.HandleFunc("/api/v1/files/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id": %d, "hash_sha256": %q}`, id, hashes[id])
	})
	mux.HandleFunc("/api/v1/files/upload", func(w http.ResponseWriter, r *http.Request) {
		if len(uploads) > 0 {
			*uploads[0]++
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": 900, "display_name": "disk.E01"}`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, _ := openrelik.NewClient(server.URL, "test-key")
	return client
}

func TestResolveInputsReusesUploads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.E01")
	os.WriteFile(path, []byte("hello"), 0o644)
	const helloSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	tests := []struct {
		name        string
		files       string
		hashes      map[int]string
		wantID      int
		wantUploads int
	}{
		{name: "same name and SHA256", files: `[{"id": 812, "display_name": "disk.E01", "filesize": 5}]`,
			hashes: map[int]string{812: helloSHA256}, wantID: 812},
		{name: "different SHA256", files: `[{"id": 812, "display_name": "disk.E01", "filesize": 5}]`,
			hashes: map[int]string{812: strings.Repeat("0", 64)}, wantID: 900, wantUploads: 1},
		{name: "no hash on the server", files: `[{"id": 812, "display_name": "disk.E01", "filesize": 5}]`,
			wantID: 900, wantUploads: 1},
		{name: "different size", files: `[{"id": 812, "display_name": "disk.E01", "filesize": 6}]`,
			hashes: map[int]string{812: helloSHA256}, wantID: 900, wantUploads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uploads := 0
			client := uploadServer(t, tt.files, tt.hashes, &uploads)
			ids, total, err := ResolveInputs(context.Background(), client, noServerPaths, []string{path}, false, 40, "")
			// The mock doesn't hash uploads, so only reused files verify.
			if tt.wantUploads == 0 && err != nil {
				t.Fatalf("ResolveInputs failed: %v", err)
			}
			if err == nil && (len(ids) != 1 || ids[0] != tt.wantID) {
				t.Errorf("IDs = %v, want [%d]", ids, tt.wantID)
			}
			if tt.wantUploads == 0 && total != 0 {
				t.Errorf("uploaded %d bytes, want 0", total)
			}
			if uploads != tt.wantUploads {
				t.Errorf("uploads = %d, want %d", uploads, tt.wantUploads)
			}
		})
	}
}

func TestVerifyUploadInfoFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, _ := openrelik.NewClient(server.URL, "test-key")
	verified, err := VerifyUpload(context.Background(), client, &openrelik.File{ID: 10}, helloSHA256)
	if verified || err == nil || !strings.Contains(err.Error(), "could not get the SHA256 of file 10") {
		t.Errorf("VerifyUpload() = %v, %v; want an error", verified, err)
	}
}

func TestResolveInputsUploadsSameFileOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk.E01")
	os.WriteFile(path, []byte("hello"), 0o644)
	uploads := 0
	client := uploadServer(t, `[]`, map[int]string{900: helloSHA256}, &uploads)
	ids, total, err := ResolveInputs(context.Background(), client, noServerPaths, []string{path, dir + "/./disk.E01"}, false, 40, "")
	if err != nil {
		t.Fatalf("ResolveInputs failed: %v", err)
	}
	if uploads != 1 || total != 5 || len(ids) != 2 || ids[0] != 900 || ids[1] != 900 {
		t.Errorf("uploads %d, total %d, IDs %v; want 1 upload of 5 bytes, IDs [900 900]", uploads, total, ids)
	}
}
