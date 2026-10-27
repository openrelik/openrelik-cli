package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	files      map[string]string // folder ID -> JSON list of its files
	info       map[string]string // file ID -> JSON file info
	listings   int               // requests for folder file listings
}

func newUploadServer(t *testing.T) *uploadServer {
	t.Helper()
	s := &uploadServer{nextID: 500, uploads: map[string]string{}, files: map[string]string{}, info: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			// A small tree for resolving --folder and --parent paths:
			// /Case A/ (42) containing /evidence/ (43).
			switch r.URL.Path {
			case "/api/v1/users/me/":
				fmt.Fprint(w, `{"id": 7, "username": "me"}`)
			case "/api/v1/folders/all/":
				// The current user (7) owns "Case A" and two "Dup" folders,
				// of which 44 was updated last; "Shared" belongs to someone else.
				fmt.Fprint(w, `{"folders": [
					{"id": 42, "display_name": "Case A", "user": {"id": 7, "username": "me"}},
					{"id": 44, "display_name": "Dup", "user": {"id": 7, "username": "me"}, "updated_at": "2026-02-01T00:00:00Z"},
					{"id": 45, "display_name": "Dup", "user": {"id": 7, "username": "me"}, "updated_at": "2026-01-01T00:00:00Z"},
					{"id": 46, "display_name": "Shared", "user": {"id": 8, "username": "other"}}
				], "page": 1, "page_size": 100, "total_count": 4}`)
			case "/api/v1/folders/42/folders/":
				fmt.Fprint(w, `[{"id": 43, "display_name": "evidence"}]`)
			case "/api/v1/folders/43/folders/":
				fmt.Fprint(w, `[]`)
			default:
				parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
				switch {
				case len(parts) == 5 && parts[2] == "folders" && parts[4] == "files":
					s.listings++
					if files, ok := s.files[parts[3]]; ok {
						fmt.Fprint(w, files)
					} else {
						fmt.Fprint(w, `[]`)
					}
				case len(parts) == 4 && parts[2] == "files" && s.info[parts[3]] != "":
					fmt.Fprint(w, s.info[parts[3]])
				default:
					w.WriteHeader(http.StatusNotFound)
				}
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
			if name == "fail.txt" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.uploads[name] = r.URL.Query().Get("folder_id")
			// The server hashes what it received; "corrupt.txt" arrives damaged.
			hash := ""
			switch name {
			case "a.txt":
				hash = aTxtSHA256
			case "corrupt.txt":
				hash = "0000"
			}
			fmt.Fprintf(w, `{"id": 900, "display_name": %q, "hash_sha256": %q}`, name, hash)
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

// aTxtSHA256 is the SHA256 of a.txt as written by writeTree.
const aTxtSHA256 = "fe599a5727285b6a0318a50aace211016346c5a82c4aed1614bd3dc03026a2c6"

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
	return runUploadWithInput(t, server, "", args...)
}

// runUploadWithInput runs 'upload' with input on stdin, for answering
// prompts.
func runUploadWithInput(t *testing.T, server *uploadServer, input string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	root := NewRootCmd()
	stdout := new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(new(bytes.Buffer))
	root.SetIn(strings.NewReader(input))
	root.SetArgs(append([]string{"upload"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func TestUpload(t *testing.T) {
	evidence := writeTree(t, "evidence", "disk.E01", "logs/syslog", "logs/deep/auth.log")
	deep := writeTree(t, "deep", "1/2/3/4/leaf.txt")
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
			args:        []string{a, "42"},
			wantUploads: map[string]string{"a.txt": "42"},
			wantOutput:  []string{"File \"a.txt\" uploaded to folder 42 (ID 900)\n  SHA256 " + aTxtSHA256 + " (verified)\n"},
		},
		{
			name:        "file the server hasn't hashed",
			args:        []string{b, "42"},
			wantUploads: map[string]string{"b.txt": "42"},
			wantOutput:  []string{"(not verified: the server has no hash for this file yet)"},
		},
		{
			name:        "files into new timestamped root folder",
			args:        []string{a, b, "/"},
			wantRoots:   []string{"Upload *"},
			wantUploads: map[string]string{"a.txt": "501", "b.txt": "501"},
			wantOutput:  []string{`(ID 501)`, `File "b.txt" uploaded to folder 501`},
		},
		{
			name:        "file into new named root folder",
			args:        []string{a, "--name", "Case 7"},
			wantRoots:   []string{"Case 7"},
			wantUploads: map[string]string{"a.txt": "501"},
			wantOutput:  []string{`Folder "Case 7" created (ID 501)`},
		},
		{
			name:        "files into new named root folder",
			args:        []string{a, b, "/", "--name", "Case 7"},
			wantRoots:   []string{"Case 7"},
			wantUploads: map[string]string{"a.txt": "501", "b.txt": "501"},
			wantOutput:  []string{`Folder "Case 7" created (ID 501)`},
		},
		{
			name:        "file into new root folder with slash",
			args:        []string{a, "/"},
			wantRoots:   []string{"Upload *"},
			wantUploads: map[string]string{"a.txt": "501"},
		},
		{
			name:           "single directory becomes the root folder",
			args:           []string{evidence},
			wantRoots:      []string{"evidence"},
			wantSubfolders: []string{"501/logs", "502/deep"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502", "auth.log": "503"},
		},
		{
			name:           "directory with slash becomes the root folder",
			args:           []string{evidence, "/"},
			wantRoots:      []string{"evidence"},
			wantSubfolders: []string{"501/logs", "502/deep"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502", "auth.log": "503"},
		},
		{
			name:        "files into folder given by path",
			args:        []string{a, b, "Case A/evidence/"},
			wantUploads: map[string]string{"a.txt": "43", "b.txt": "43"},
			wantOutput:  []string{`File "a.txt" uploaded to folder 43`},
		},
		{
			name:           "directory into existing folder is created inside it",
			args:           []string{evidence, "42"},
			wantSubfolders: []string{"42/evidence", "501/logs", "502/deep"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502", "auth.log": "503"},
		},
		{
			name:           "depth limits recursion",
			args:           []string{evidence, "--depth", "1"},
			wantRoots:      []string{"evidence"},
			wantSubfolders: []string{"501/logs"},
			wantUploads:    map[string]string{"disk.E01": "501", "syslog": "502"},
			wantOutput:     []string{`Not uploaded "` + filepath.Join(evidence, "logs", "deep") + `/": deeper than --depth 1`},
		},
		{
			name:           "all levels are uploaded by default",
			args:           []string{deep},
			wantRoots:      []string{"deep"},
			wantSubfolders: []string{"501/1", "502/2", "503/3", "504/4"},
			wantUploads:    map[string]string{"leaf.txt": "505"},
		},
		{
			name:           "mixed files and directories into new root folder",
			args:           []string{a, evidence, "/"},
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
	loose := writeTree(t, "loose", "a.txt", "b.txt", "corrupt.txt")
	a, b := filepath.Join(loose, "a.txt"), filepath.Join(loose, "b.txt")
	corrupt := filepath.Join(loose, "corrupt.txt")

	tests := []struct {
		name     string
		args     []string
		wantErr  string
		uploaded bool // the error comes after the file reached the server
	}{
		{name: "missing path creates nothing", args: []string{a, filepath.Join(loose, "missing"), "/"}, wantErr: "no such file"},
		{name: "name with existing folder", args: []string{a, "42", "--name", "x"}, wantErr: "--name only applies when creating a new root folder"},
		{name: "invalid folder ID", args: []string{a, "0"}, wantErr: "invalid folder ID"},
		{name: "unknown folder path", args: []string{a, "Case A/nope/"}, wantErr: `no folder named "nope" in /Case A/`},
		{name: "forgotten destination", args: []string{a, b}, wantErr: "is also a local path. Add the destination folder at the end"},
		{name: "filesystem root", args: []string{"/"}, wantErr: `refusing to upload the filesystem root "/"`},
		{name: "old --folder flag points to FOLDER", args: []string{a, "--folder", "42"}, wantErr: "'openrelik upload --folder' was removed. Use instead:\n\n  openrelik upload <PATH>... <FOLDER>"},
		{name: "old --folder= flag points to FOLDER", args: []string{a, "--folder=Case A/"}, wantErr: "'openrelik upload --folder' was removed"},
		{name: "no paths", args: []string{}, wantErr: "PATH"},
		{name: "negative depth", args: []string{a, "--depth", "-1"}, wantErr: "invalid depth: -1"},
		{name: "zero chunk size", args: []string{a, "--chunk-size", "0"}, wantErr: "invalid chunk size: 0"},
		{name: "server hash mismatch", args: []string{corrupt, "42"}, uploaded: true, wantErr: "file 900 on the server doesn't match what was uploaded: its SHA256 is 0000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()

			_, err := runUpload(t, server, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if tt.uploaded {
				return
			}
			if len(server.roots)+len(server.subfolders)+len(server.uploads) > 0 {
				t.Errorf("nothing should be created on error: roots %q, subfolders %q, uploads %v",
					server.roots, server.subfolders, server.uploads)
			}
		})
	}
}

func TestUploadSkipsDuplicates(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt", "b.txt")
	a, b := filepath.Join(loose, "a.txt"), filepath.Join(loose, "b.txt")
	content := "content of a.txt"
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])

	existing := func(name string, size int) string {
		return fmt.Sprintf(`[{"id": 77, "display_name": %q, "filesize": %d}]`, name, size)
	}
	tests := []struct {
		name        string
		args        []string
		files       string // JSON listing of folder 42
		hash        string // SHA256 of file 77 on the server
		wantUploads map[string]string
		wantOutput  []string
		wantSkipped bool
	}{
		{
			name:        "same name and SHA256 is skipped",
			args:        []string{a, b, "42"},
			files:       existing("a.txt", len(content)),
			hash:        hash,
			wantUploads: map[string]string{"b.txt": "42"},
			wantOutput:  []string{`Skipped "a.txt": already in folder 42 as file 77 (same name and SHA256)`},
			wantSkipped: true,
		},
		{
			name:        "hash is compared case-insensitively",
			args:        []string{a, "42"},
			files:       existing("a.txt", len(content)),
			hash:        strings.ToUpper(hash),
			wantUploads: map[string]string{},
			wantSkipped: true,
		},
		{
			name:        "different content is uploaded",
			args:        []string{a, "42"},
			files:       existing("a.txt", len(content)),
			hash:        strings.Repeat("0", 64),
			wantUploads: map[string]string{"a.txt": "42"},
		},
		{
			name:        "different size is uploaded",
			args:        []string{a, "42"},
			files:       existing("a.txt", len(content)+1),
			wantUploads: map[string]string{"a.txt": "42"},
		},
		{
			name:        "different name is uploaded",
			args:        []string{a, "42"},
			files:       existing("other.txt", len(content)),
			wantUploads: map[string]string{"a.txt": "42"},
		},
		{
			name:        "force uploads anyway",
			args:        []string{a, "42", "--force"},
			files:       existing("a.txt", len(content)),
			hash:        hash,
			wantUploads: map[string]string{"a.txt": "42"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()
			server.files["42"] = tt.files
			server.info["77"] = fmt.Sprintf(`{"id": 77, "display_name": "a.txt", "hash_sha256": %q}`, tt.hash)

			out, err := runUpload(t, server, tt.args...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if fmt.Sprint(server.uploads) != fmt.Sprint(tt.wantUploads) {
				t.Errorf("uploaded %v, want %v", server.uploads, tt.wantUploads)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q\n%s", want, out)
				}
			}
			if got := strings.Contains(out, "Skipped"); got != tt.wantSkipped {
				t.Errorf("skipped = %v, want %v\n%s", got, tt.wantSkipped, out)
			}
		})
	}
}

func TestUploadUnverifiedDuplicates(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt", "b.txt")
	a, b := filepath.Join(loose, "a.txt"), filepath.Join(loose, "b.txt")

	tests := []struct {
		name        string
		terminal    bool
		input       string
		wantErr     string
		wantUploads map[string]string
		wantOutput  string
	}{
		{
			name:        "fails without a terminal",
			wantErr:     "\"a.txt\" may already be in folder 42 as file 77: it has the same name and size, but the server has no hash to compare.\n\nUse --force",
			wantUploads: map[string]string{},
		},
		{
			name:        "uploads when confirmed",
			terminal:    true,
			input:       "y\n",
			wantUploads: map[string]string{"a.txt": "42", "b.txt": "42"},
		},
		{
			name:        "skips when declined",
			terminal:    true,
			input:       "n\n",
			wantUploads: map[string]string{"b.txt": "42"},
			wantOutput:  `Skipped "a.txt": already in folder 42 as file 77 (same name and size; the server has no hash to compare)`,
		},
		{
			name:        "skips at end of input",
			terminal:    true,
			wantUploads: map[string]string{"b.txt": "42"},
			wantOutput:  `Skipped "a.txt"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := stdinIsTerminal
			stdinIsTerminal = func(io.Reader) bool { return tt.terminal }
			t.Cleanup(func() { stdinIsTerminal = orig })

			server := newUploadServer(t)
			defer server.Close()
			server.files["42"] = fmt.Sprintf(`[{"id": 77, "display_name": "a.txt", "filesize": %d}]`, len("content of a.txt"))
			server.info["77"] = `{"id": 77, "display_name": "a.txt"}`

			out, err := runUploadWithInput(t, server, tt.input, a, b, "42")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if fmt.Sprint(server.uploads) != fmt.Sprint(tt.wantUploads) {
				t.Errorf("uploaded %v, want %v", server.uploads, tt.wantUploads)
			}
			if !strings.Contains(out, tt.wantOutput) {
				t.Errorf("expected output to contain %q\n%s", tt.wantOutput, out)
			}
		})
	}
}

func TestUploadPrefersHashMatchOverUnhashed(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt")
	content := "content of a.txt"
	sum := sha256.Sum256([]byte(content))

	server := newUploadServer(t)
	defer server.Close()
	server.files["42"] = fmt.Sprintf(`[{"id": 76, "display_name": "a.txt", "filesize": %d}, {"id": 77, "display_name": "a.txt", "filesize": %d}]`,
		len(content), len(content))
	server.info["76"] = `{"id": 76, "display_name": "a.txt"}`
	server.info["77"] = fmt.Sprintf(`{"id": 77, "display_name": "a.txt", "hash_sha256": %q}`, hex.EncodeToString(sum[:]))

	// No terminal, so asking about file 76 would fail.
	out, err := runUpload(t, server, filepath.Join(loose, "a.txt"), "42")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if want := `Skipped "a.txt": already in folder 42 as file 77 (same name and SHA256)`; !strings.Contains(out, want) {
		t.Errorf("expected output to contain %q\n%s", want, out)
	}
}

func TestUploadForgottenDestinationHint(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "Dup", "missing"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	for _, tt := range []struct {
		name     string
		dest     string
		wantHint bool
	}{
		{name: "no such folder", dest: "missing", wantHint: true},
		{name: "ambiguous folder", dest: "Dup"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()
			_, err := runUpload(t, server, "a.txt", tt.dest)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := strings.Contains(err.Error(), "is also a local path"); got != tt.wantHint {
				t.Errorf("hint = %v, want %v: %v", got, tt.wantHint, err)
			}
		})
	}
}

func TestUploadDuplicateCheckScope(t *testing.T) {
	evidence := writeTree(t, "evidence", "disk.E01")
	loose := writeTree(t, "loose", "a.txt")
	a := filepath.Join(loose, "a.txt")

	for _, tt := range []struct {
		name         string
		args         []string
		wantListings int
	}{
		{name: "new root folder", args: []string{a, "/"}},
		{name: "single argument", args: []string{a}},
		{name: "only directories", args: []string{evidence, "42"}},
		{name: "files into existing folder", args: []string{a, "42"}, wantListings: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()
			if _, err := runUpload(t, server, tt.args...); err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if server.listings != tt.wantListings {
				t.Errorf("listed folder files %d times, want %d", server.listings, tt.wantListings)
			}
		})
	}
}

func TestUploadJSONIncludesSkipped(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt")
	server := newUploadServer(t)
	defer server.Close()
	sum := sha256.Sum256([]byte("content of a.txt"))
	server.files["42"] = `[{"id": 77, "display_name": "a.txt", "filesize": 16}]`
	server.info["77"] = fmt.Sprintf(`{"id": 77, "display_name": "a.txt", "hash_sha256": %q}`, hex.EncodeToString(sum[:]))

	out, err := runUpload(t, server, filepath.Join(loose, "a.txt"), "42", "--format", "json")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	var got struct {
		Uploads []any         `json:"uploads"`
		Skipped []skippedFile `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(got.Uploads) != 0 || len(got.Skipped) != 1 || got.Skipped[0].FileID != 77 || got.Skipped[0].FolderID != 42 ||
		!strings.HasSuffix(got.Skipped[0].Path, "a.txt") || got.Skipped[0].Reason == "" {
		t.Errorf("unexpected JSON: %s", out)
	}
}

func TestUploadVerboseSkipped(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt", "b.txt")
	sum := sha256.Sum256([]byte("content of a.txt"))
	tests := []struct {
		name      string
		args      []string
		wantStart string
	}{
		{name: "all skipped", args: []string{"a.txt"}, wantStart: `Skipped "a.txt"`},
		{name: "some skipped", args: []string{"a.txt", "b.txt"}, wantStart: "ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()
			server.files["42"] = `[{"id": 77, "display_name": "a.txt", "filesize": 16}]`
			server.info["77"] = fmt.Sprintf(`{"id": 77, "display_name": "a.txt", "hash_sha256": %q}`, hex.EncodeToString(sum[:]))

			var args []string
			for _, a := range tt.args {
				args = append(args, filepath.Join(loose, a))
			}
			out, err := runUpload(t, server, append(args, "42", "--format", "verbose")...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if !strings.HasPrefix(out, tt.wantStart) {
				t.Errorf("expected output to start with %q\n%q", tt.wantStart, out)
			}
			if !strings.Contains(out, `Skipped "a.txt"`) {
				t.Errorf("expected a.txt to be reported as skipped\n%s", out)
			}
		})
	}
}

func TestUploadPartialFailureNamesCreatedFolder(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt", "fail.txt")
	server := newUploadServer(t)
	defer server.Close()

	_, err := runUpload(t, server, filepath.Join(loose, "a.txt"), filepath.Join(loose, "fail.txt"), "/", "--name", "Case")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	want := `Folder "Case" (ID 501) was created and holds the files uploaded before the error.`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("expected error to contain %q, got %q", want, err.Error())
	}
	if server.uploads["a.txt"] != "501" {
		t.Errorf("a.txt should be uploaded before the failure, got %v", server.uploads)
	}
}

func TestUploadDotUsesDirectoryName(t *testing.T) {
	evidence := writeTree(t, "evidence", "disk.E01")
	t.Chdir(evidence)

	for _, args := range [][]string{{"."}, {".", "42"}} {
		server := newUploadServer(t)
		if _, err := runUpload(t, server, args...); err != nil {
			t.Fatalf("%v: Execute() failed: %v", args, err)
		}
		created := append(server.roots, server.subfolders...)
		if len(created) != 1 || !strings.HasSuffix(created[0], "evidence") {
			t.Errorf("%v: created folders %q, want one named evidence", args, created)
		}
		server.Close()
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
		{[]string{"/cases/evidence/logs/.."}, []bool{true}, "evidence"},
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
			fmt.Fprintf(w, `{"id": 789, "display_name": "test.txt", "filesize": 13, "hash_sha256": %q}`, helloWorldSHA256)
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
		root.SetArgs([]string{"download", "789"})

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
		want := "File 789 saved to \"test.txt\"\n  SHA256 " + helloWorldSHA256 + " (verified)\n"
		if buf.String() != want {
			t.Errorf("output = %q, want %q", buf.String(), want)
		}
	})

	t.Run("specific directory", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "openrelik-test-*")
		defer os.RemoveAll(tmpDir)

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"download", "789", tmpDir})

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
		root.SetArgs([]string{"download", "789", customPath})

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
		root.SetArgs([]string{"download", "789", "/non/existent/path/file.txt"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for non-existent folder")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("expected 'does not exist' error, got %v", err)
		}
	})

	orig := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	defer func() { stdinIsTerminal = orig }()

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
		root.SetArgs([]string{"download", "789", tmpFile.Name()})

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
		root.SetArgs([]string{"download", "789", tmpFile.Name()})

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

	t.Run("existing file without a terminal", func(t *testing.T) {
		stdinIsTerminal = func(io.Reader) bool { return false }
		defer func() { stdinIsTerminal = func(io.Reader) bool { return true } }()
		dest := filepath.Join(t.TempDir(), "test.txt")
		os.WriteFile(dest, []byte("old content"), 0o644)

		root := NewRootCmd()
		root.SetOut(new(bytes.Buffer))
		root.SetErr(new(bytes.Buffer))
		root.SetIn(strings.NewReader("y\n"))
		root.SetArgs([]string{"download", "789", dest})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "already exists; use --force to replace it") {
			t.Fatalf("expected an error asking for --force, got %v", err)
		}
		if content, _ := os.ReadFile(dest); string(content) != "old content" {
			t.Errorf("existing file was changed: %q", content)
		}
	})

	t.Run("force replaces without asking", func(t *testing.T) {
		stdinIsTerminal = func(io.Reader) bool { return false }
		defer func() { stdinIsTerminal = func(io.Reader) bool { return true } }()
		dest := filepath.Join(t.TempDir(), "test.txt")
		os.WriteFile(dest, []byte("old content"), 0o644)

		root := NewRootCmd()
		root.SetOut(new(bytes.Buffer))
		root.SetErr(new(bytes.Buffer))
		root.SetArgs([]string{"download", "789", dest, "--force"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}
		if content, _ := os.ReadFile(dest); string(content) != "hello world!!" {
			t.Errorf("expected the file to be replaced, got %q", content)
		}
	})
}

// helloWorldSHA256 is the SHA256 of "hello world!!".
const helloWorldSHA256 = "8380c4c6720e0d5ce4789bf72df03a6e1b3ed80891f3adbe8833c760399b8e91"

// TestDownloadHashMismatch checks that a download whose data doesn't match
// the server's SHA256 leaves nothing behind, and doesn't touch an existing
// file.
func TestDownloadHashMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/files/789":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id": 789, "display_name": "test.txt", "filesize": 13, "hash_sha256": "0000"}`)
		case "/api/v1/files/789/download":
			fmt.Fprint(w, "hello world!!")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%v", existing), func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "test.txt")
			if existing {
				os.WriteFile(dest, []byte("old content"), 0o644)
			}
			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs([]string{"download", "789", dest, "--force"})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), "was not saved: its SHA256 on the server is 0000") {
				t.Fatalf("expected a hash mismatch error, got %v", err)
			}
			entries, _ := os.ReadDir(dir)
			if !existing && len(entries) != 0 {
				t.Errorf("files were left behind: %v", entries)
			}
			if existing {
				if len(entries) != 1 {
					t.Errorf("files were left behind: %v", entries)
				}
				if content, _ := os.ReadFile(dest); string(content) != "old content" {
					t.Errorf("existing file was changed: %q", content)
				}
			}
		})
	}
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
		{name: "missing file", args: []string{"download"}, wantErr: "missing required argument: FILE"},
		{name: "zero file ID", args: []string{"download", "0"}, wantErr: "invalid file ID: 0"},
		{name: "name without folder", args: []string{"download", "abc"}, wantErr: `invalid file "abc"`},
		{name: "two destinations", args: []string{"download", "1", "a", "b"}, wantErr: "too many arguments: expected at most 2, got 3\nQuote FILE and DESTINATION if they contain spaces."},
		{name: "unquoted path", args: []string{"download", "Case", "A/notes.txt"}, wantErr: `Quote FILE if it contains spaces: "Case A/notes.txt"`},
		{name: "old --file flag points to FILE", args: []string{"download", "--file", "1"}, wantErr: "'openrelik download --file' was removed. Use instead:\n\n  openrelik download <FILE> [DESTINATION]"},
		{name: "old file download points to download", args: []string{"file", "download", "1"}, wantErr: "openrelik download <FILE> [DESTINATION]"},
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

// TestDownloadStaysInDirectory checks that a hostile display name from the
// server can't make 'download' write outside the current or given directory.
func TestDownloadStaysInDirectory(t *testing.T) {
	var displayName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/files/789":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id": 789, "display_name": %q, "filesize": 6}`, displayName)
		case "/api/v1/files/789/download":
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "PWNED\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	// base/work is the working directory and base/out a download directory;
	// base/outside.txt must never be written.
	base := t.TempDir()
	work, outDir := filepath.Join(base, "work"), filepath.Join(base, "out")
	outside := filepath.Join(base, "outside.txt")
	for _, d := range []string{work, outDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(work)

	tests := []struct {
		name        string
		displayName string
		args        []string
		setup       func(t *testing.T) // runs in work before the download
		wantFile    string             // path that should hold the download
		wantErr     string
	}{
		{name: "parent directory name", displayName: "../outside.txt", wantFile: filepath.Join(work, "outside.txt")},
		{name: "absolute name", displayName: outside, wantFile: filepath.Join(work, "outside.txt")},
		{name: "parent directory name into directory", displayName: "../outside.txt", args: []string{outDir}, wantFile: filepath.Join(outDir, "outside.txt")},
		{name: "dot dot", displayName: "..", wantErr: `name ".." can't be used as a local file name; give DESTINATION`},
		{name: "root", displayName: "/", wantErr: "can't be used as a local file name"},
		{name: "empty", displayName: "", wantErr: "can't be used as a local file name"},
		{
			name:        "existing symlink is not followed",
			displayName: "link.txt",
			setup: func(t *testing.T) {
				os.WriteFile(outside, []byte("original"), 0o644)
				os.Symlink(outside, "link.txt")
			},
			wantErr: `"link.txt" is a symlink; refusing to write through it`,
		},
		{
			name:        "dangling symlink is not followed",
			displayName: "dangling.txt",
			setup:       func(t *testing.T) { os.Symlink(outside, "dangling.txt") },
			wantErr:     "is a symlink",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, d := range []string{work, outDir} {
				entries, _ := os.ReadDir(d)
				for _, e := range entries {
					os.Remove(filepath.Join(d, e.Name()))
				}
			}
			os.Remove(outside)
			if tt.setup != nil {
				tt.setup(t)
			}
			before, _ := os.ReadFile(outside)
			displayName = tt.displayName

			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetIn(strings.NewReader("y\n"))
			root.SetArgs(append([]string{"download", "789", "-q"}, tt.args...))
			err := root.Execute()

			if after, _ := os.ReadFile(outside); string(after) != string(before) {
				t.Errorf("%s was written: %q", outside, after)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if got, _ := os.ReadFile(tt.wantFile); string(got) != "PWNED\n" {
				t.Errorf("expected the download in %s, got %q", tt.wantFile, got)
			}
		})
	}
}

func TestUploadRepeatedPathOnce(t *testing.T) {
	loose := writeTree(t, "loose", "a.txt")
	a := filepath.Join(loose, "a.txt")
	// The same file under another spelling of its path.
	other := filepath.Join(loose, ".", "a.txt")
	content := "content of a.txt"
	sum := sha256.Sum256([]byte(content))

	tests := []struct {
		name     string
		args     []string
		files    string // JSON listing of folder 42
		wantLine string
	}{
		{
			name:     "new file is uploaded once",
			args:     []string{a, other, "42"},
			wantLine: `File "a.txt" uploaded`,
		},
		{
			name:     "existing file is skipped once",
			args:     []string{a, a, "42"},
			files:    fmt.Sprintf(`[{"id": 77, "display_name": "a.txt", "filesize": %d}]`, len(content)),
			wantLine: `Skipped "a.txt"`,
		},
		{
			name:     "directory is uploaded once",
			args:     []string{loose, loose + "/", "42"},
			wantLine: `File "a.txt" uploaded`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()
			if tt.files != "" {
				server.files["42"] = tt.files
			}
			server.info["77"] = fmt.Sprintf(`{"id": 77, "display_name": "a.txt", "hash_sha256": %q}`, hex.EncodeToString(sum[:]))

			out, err := runUpload(t, server, tt.args...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if n := strings.Count(out, tt.wantLine); n != 1 {
				t.Errorf("expected %q once, got %d times\n%s", tt.wantLine, n, out)
			}
			if len(server.subfolders) > 1 {
				t.Errorf("created subfolders %q, want at most one", server.subfolders)
			}
		})
	}
}
