package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openrelik/openrelik-cli/config"
)

// uploadServer is a mock API that records the folders created and the files
// uploaded by 'upload'.
type uploadServer struct {
	*httptest.Server
	mu         sync.Mutex
	nextID     int
	roots      []string          // names of created root folders
	subfolders []string          // "parentID/name" of created subfolders
	uploads    map[string]string // filename -> folder_id
}

func newUploadServer(t *testing.T) *uploadServer {
	t.Helper()
	s := &uploadServer{nextID: 500, uploads: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			// A small tree for resolving --folder and --parent paths:
			// /Case A/ (42) containing /evidence/ (43).
			switch r.URL.Path {
			case "/api/v1/folders/all/":
				fmt.Fprint(w, `{"folders": [{"id": 42, "display_name": "Case A"}], "page": 1, "page_size": 100, "total_count": 1}`)
			case "/api/v1/folders/42/folders/":
				fmt.Fprint(w, `[{"id": 43, "display_name": "evidence"}]`)
			case "/api/v1/folders/43/folders/":
				fmt.Fprint(w, `[]`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/files/upload":
			name := r.URL.Query().Get("resumableFilename")
			s.uploads[name] = r.URL.Query().Get("folder_id")
			fmt.Fprintf(w, `{"id": 900, "display_name": %q}`, name)
		case r.URL.Path == "/api/v1/folders/" || strings.HasSuffix(r.URL.Path, "/folders/"):
			var body struct {
				DisplayName string `json:"display_name"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			s.nextID++
			if r.URL.Path == "/api/v1/folders/" {
				s.roots = append(s.roots, body.DisplayName)
			} else {
				parent := strings.Split(r.URL.Path, "/")[4]
				s.subfolders = append(s.subfolders, parent+"/"+body.DisplayName)
			}
			fmt.Fprintf(w, `{"id": %d, "display_name": %q}`, s.nextID, body.DisplayName)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return s
}

// writeTree creates files (relative paths) with some content under a new
// temp dir named dirName and returns the dir's path.
func writeTree(t *testing.T, dirName string, files ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), dirName)
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runUpload(t *testing.T, server *uploadServer, args ...string) (string, error) {
	t.Helper()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	root := NewRootCmd()
	stdout := new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(append([]string{"upload"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func TestUpload(t *testing.T) {
	evidence := writeTree(t, "evidence", "disk.E01", "logs/syslog", "logs/deep/auth.log")
	loose := writeTree(t, "loose", "a.txt", "b.txt")
	a, b := filepath.Join(loose, "a.txt"), filepath.Join(loose, "b.txt")

	tests := []struct {
		name           string
		args           []string
		wantRoots      []string // exact names; "Upload *" matches the timestamp name
		wantSubfolders []string
		wantUploads    map[string]string
		wantOutput     []string
	}{
		{
			name:        "file into existing folder",
			args:        []string{a, "--folder", "42"},
			wantUploads: map[string]string{"a.txt": "42"},
			wantOutput:  []string{`File "a.txt" uploaded to folder 42 (ID 900)`},
		},
		{
			name:        "files into new timestamped root folder",
			args:        []string{a, b},
			wantRoots:   []string{"Upload *"},
			wantUploads: map[string]string{"a.txt": "501", "b.txt": "501"},
			wantOutput:  []string{`(ID 501)`, `File "b.txt" uploaded to folder 501`},
		},
		{
			name:        "files into new named root folder",
			args:        []string{a, "--name", "Case 7"},
			wantRoots:   []string{"Case 7"},
			wantUploads: map[string]string{"a.txt": "501"},
			wantOutput:  []string{`Folder "Case 7" created (ID 501)`},
		},
		{
			name:           "single directory becomes the root folder",
			args:           []string{evidence},
			wantRoots:      []string{"evidence"},
			wantSubfolders: []string{"501/logs", "502/deep"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502", "auth.log": "503"},
		},
		{
			name:        "file into folder given by path",
			args:        []string{a, "--folder", "Case A/evidence/"},
			wantUploads: map[string]string{"a.txt": "43"},
			wantOutput:  []string{`File "a.txt" uploaded to folder 43`},
		},
		{
			name:           "directory into existing folder is created inside it",
			args:           []string{evidence, "--folder", "42"},
			wantSubfolders: []string{"42/evidence", "501/logs", "502/deep"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502", "auth.log": "503"},
		},
		{
			name:           "depth limits recursion",
			args:           []string{evidence, "--depth", "1"},
			wantRoots:      []string{"evidence"},
			wantSubfolders: []string{"501/logs"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502"},
		},
		{
			name:           "mixed files and directories into new root folder",
			args:           []string{a, evidence},
			wantRoots:      []string{"Upload *"},
			wantSubfolders: []string{"501/evidence", "502/logs", "503/deep"},
			wantUploads:    map[string]string{"a.txt": "501", "disk.E01": "502", "syslog": "503", "auth.log": "504"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()

			out, err := runUpload(t, server, tt.args...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}

			if len(server.roots) != len(tt.wantRoots) {
				t.Fatalf("created root folders %q, want %q", server.roots, tt.wantRoots)
			}
			for i, want := range tt.wantRoots {
				got := server.roots[i]
				if want == "Upload *" {
					if _, err := time.Parse("Upload 2006-01-02 15:04", got); err != nil {
						t.Errorf("root folder %q is not a timestamp name: %v", got, err)
					}
				} else if got != want {
					t.Errorf("created root folder %q, want %q", got, want)
				}
			}
			if strings.Join(server.subfolders, ",") != strings.Join(tt.wantSubfolders, ",") {
				t.Errorf("created subfolders %q, want %q", server.subfolders, tt.wantSubfolders)
			}
			if len(server.uploads) != len(tt.wantUploads) {
				t.Errorf("uploaded %v, want %v", server.uploads, tt.wantUploads)
			}
			for file, folder := range tt.wantUploads {
				if got := server.uploads[file]; got != folder {
					t.Errorf("%s uploaded to folder %q, want %q", file, got, folder)
				}
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q\n%s", want, out)
				}
			}
		})
	}
}

func TestUploadErrors(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt")
	a := filepath.Join(loose, "a.txt")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing path creates nothing", args: []string{a, filepath.Join(loose, "missing")}, wantErr: "no such file"},
		{name: "folder and name conflict", args: []string{a, "--folder", "42", "--name", "x"}, wantErr: "none of the others can be"},
		{name: "invalid folder ID", args: []string{a, "--folder", "0"}, wantErr: "invalid folder ID"},
		{name: "unknown folder path", args: []string{a, "--folder", "Case A/nope/"}, wantErr: `no folder named "nope" in /Case A/`},
		{name: "no paths", args: []string{}, wantErr: "PATH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()

			_, err := runUpload(t, server, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if len(server.roots)+len(server.subfolders)+len(server.uploads) > 0 {
				t.Errorf("nothing should be created on error: roots %q, subfolders %q, uploads %v",
					server.roots, server.subfolders, server.uploads)
			}
		})
	}
}

func TestUploadJSON(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt")
	server := newUploadServer(t)
	defer server.Close()

	out, err := runUpload(t, server, filepath.Join(loose, "a.txt"), "--name", "Case", "--format", "json")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	var got struct {
		FolderID int `json:"folder_id"`
		Created  struct {
			ID          int    `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"created_folder"`
		Uploads []struct {
			File     struct{ ID int } `json:"file"`
			FolderID int              `json:"folder_id"`
		} `json:"uploads"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.FolderID != 501 || got.Created.DisplayName != "Case" || len(got.Uploads) != 1 ||
		got.Uploads[0].File.ID != 900 || got.Uploads[0].FolderID != 501 {
		t.Errorf("unexpected JSON: %s", out)
	}
}

func TestDefaultUploadFolderName(t *testing.T) {
	now := time.Date(2026, 10, 27, 14, 5, 0, 0, time.UTC)
	tests := []struct {
		paths []string
		isDir []bool
		want  string
	}{
		{[]string{"./evidence/"}, []bool{true}, "evidence"},
		{[]string{"/cases/disk.E01"}, []bool{false}, "Upload 2026-10-27 14:05"},
		{[]string{"a", "b"}, []bool{true, true}, "Upload 2026-10-27 14:05"},
	}
	for _, tt := range tests {
		if got := defaultUploadFolderName(tt.paths, tt.isDir, now); got != tt.want {
			t.Errorf("defaultUploadFolderName(%q) = %q, want %q", tt.paths, got, tt.want)
		}
	}
}

func TestDownloadCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/files/789" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 789, "display_name": "test.txt", "filesize": 13}`)
			return
		}
		if r.URL.Path == "/api/v1/files/789/download" {
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "hello world!!")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	os.Setenv("OPENRELIK_API_KEY", "test-key")
	os.Setenv("OPENRELIK_SERVER_URL", server.URL)
	defer func() {
		os.Unsetenv("OPENRELIK_API_KEY")
		os.Unsetenv("OPENRELIK_SERVER_URL")
	}()

	t.Run("default current directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "openrelik-test-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get current directory: %v", err)
		}
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change directory: %v", err)
		}
		defer os.Chdir(oldWd)

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"download", "--file", "789"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, err := os.ReadFile("test.txt")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}
		if string(content) != "hello world!!" {
			t.Errorf("expected 'hello world!!', got %q", string(content))
		}
	})

	t.Run("specific directory", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "openrelik-test-*")
		defer os.RemoveAll(tmpDir)

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"download", "--file", "789", tmpDir})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, _ := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
		if string(content) != "hello world!!" {
			t.Errorf("expected 'hello world!!', got %q", string(content))
		}
	})

	t.Run("specific file path", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "openrelik-test-*")
		defer os.RemoveAll(tmpDir)
		customPath := filepath.Join(tmpDir, "custom.bin")

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"download", "--file", "789", customPath})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, _ := os.ReadFile(customPath)
		if string(content) != "hello world!!" {
			t.Errorf("expected 'hello world!!', got %q", string(content))
		}
	})

	t.Run("non-existent folder", func(t *testing.T) {
		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"download", "--file", "789", "/non/existent/path/file.txt"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for non-existent folder")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("expected 'does not exist' error, got %v", err)
		}
	})

	t.Run("overwrite confirmation - yes", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "openrelik-overwrite-*")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("old content")
		tmpFile.Close()

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		// Mock "y" input
		root.SetIn(strings.NewReader("y\n"))
		root.SetArgs([]string{"download", "--file", "789", tmpFile.Name()})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, _ := os.ReadFile(tmpFile.Name())
		if string(content) != "hello world!!" {
			t.Errorf("expected overwritten content 'hello world!!', got %q", string(content))
		}
	})

	t.Run("overwrite confirmation - empty (Enter)", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "openrelik-overwrite-*")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("old content")
		tmpFile.Close()

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		// Mock empty input (just newline)
		root.SetIn(strings.NewReader("\n"))
		root.SetArgs([]string{"download", "--file", "789", tmpFile.Name()})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for cancelled download")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("expected 'cancelled' error, got %v", err)
		}

		content, _ := os.ReadFile(tmpFile.Name())
		if string(content) != "old content" {
			t.Errorf("expected original content 'old content', got %q", string(content))
		}
	})
}

func TestDownloadArgErrors(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", "http://127.0.0.1:1")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing --file", args: []string{"download"}, wantErr: `required flag(s) "file" not set`},
		{name: "zero file ID", args: []string{"download", "--file", "0"}, wantErr: "invalid file ID: 0"},
		{name: "non-numeric file ID", args: []string{"download", "--file", "abc"}, wantErr: `invalid argument "abc"`},
		{name: "two destinations", args: []string{"download", "--file", "1", "a", "b"}, wantErr: "too many arguments"},
		{name: "old file download is gone", args: []string{"file", "download", "1"}, wantErr: `unknown command "download" for "openrelik file"`},
		{name: "old folder upload is gone", args: []string{"folder", "upload", "x"}, wantErr: `unknown command "upload" for "openrelik folder"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
