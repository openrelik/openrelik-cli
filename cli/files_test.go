package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
)

// newCatServer serves the download and info endpoints for the files in
// contents. File 500 fails with a server error and unknown IDs return 404.
// Requests for file endpoints are counted in requests.
//
// It also serves a folder tree for resolving paths:
//
//	/Case A/            (1)  notes.txt (1), dup.txt (2), dup.txt (3)
//	/Case A/evidence/   (11) disk.bin (3)
func newCatServer(t *testing.T, contents map[int]string, requests *int) *httptest.Server {
	t.Helper()
	folder := func(id int, name string) string {
		return fmt.Sprintf(`{"id": %d, "display_name": %q, "user": {"username": "dev"}}`, id, name)
	}
	file := func(id int, name string) string {
		return fmt.Sprintf(`{"id": %d, "display_name": %q, "filesize": %d}`, id, name, len(contents[id]))
	}
	subfolders := map[string][]string{"1": {folder(11, "evidence")}}
	files := map[string][]string{
		"1":  {file(1, "notes.txt"), file(2, "dup.txt"), file(3, "dup.txt")},
		"11": {file(3, "disk.bin")},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.URL.Path == "/api/v1/folders/all/":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"folders": [%s], "page": 1, "page_size": 100, "total_count": 1}`, folder(1, "Case A"))
			return
		case len(parts) == 5 && parts[2] == "folders" && parts[4] == "folders":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "[%s]", strings.Join(subfolders[parts[3]], ","))
			return
		case len(parts) == 5 && parts[2] == "folders" && parts[4] == "files":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "[%s]", strings.Join(files[parts[3]], ","))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/v1/files/") {
			*requests++
		}
		for id, content := range contents {
			switch r.URL.Path {
			case fmt.Sprintf("/api/v1/files/%d/download", id):
				w.Write([]byte(content))
				return
			case fmt.Sprintf("/api/v1/files/%d", id):
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id": %d, "display_name": "file-%d.bin"}`, id, id)
				return
			}
		}
		if r.URL.Path == "/api/v1/files/500/download" {
			http.Error(w, `{"detail": "boom"}`, http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// runCat executes 'file cat' with args against server and returns stdout,
// stderr and the error from Execute.
func runCat(t *testing.T, server *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	config.SetBaseDir(t.TempDir())
	t.Cleanup(func() { config.SetBaseDir("") })
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"file", "cat"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// fakeTerminal makes stdout count as a terminal for the rest of the test.
func fakeTerminal(t *testing.T) {
	t.Helper()
	orig := stdoutIsTerminal
	stdoutIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { stdoutIsTerminal = orig })
}

const (
	catText   = "hello\nworld\n"
	catBinary = "MZ\x00\x01\x02binary\x00data"
)

// catLargeText is longer than the binary peek, so printing it to a terminal
// covers reading past the peeked bytes.
var catLargeText = strings.Repeat("line of text\n", 2*binaryPeekSize/13)

func catContents() map[int]string {
	return map[int]string{1: catText, 2: "second\n", 3: catBinary, 4: catLargeText}
}

func TestFileCat(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		terminal bool
		want     string
	}{
		{name: "single text file", args: []string{"1"}, want: catText},
		{name: "several files in argument order", args: []string{"2", "1", "2"}, want: "second\n" + catText + "second\n"},
		{name: "binary passes through when piped", args: []string{"3"}, want: catBinary},
		{name: "text prints to a terminal", args: []string{"1"}, terminal: true, want: catText},
		{name: "large text prints to a terminal", args: []string{"4"}, terminal: true, want: catLargeText},
		{name: "path", args: []string{"Case A/notes.txt"}, want: catText},
		{name: "path with leading slash", args: []string{"/Case A/evidence/disk.bin"}, want: catBinary},
		{name: "case-insensitive path", args: []string{"case a/NOTES.TXT"}, want: catText},
		{name: "paths and IDs mixed", args: []string{"2", "/Case A/notes.txt"}, want: "second\n" + catText},
		{name: "number lines", args: []string{"-n", "1"}, want: "     1\thello\n     2\tworld\n"},
		{name: "numbering continues across files", args: []string{"1", "--number", "2"}, want: "     1\thello\n     2\tworld\n     3\tsecond\n"},
		{name: "first bytes", args: []string{"-c", "3", "1"}, want: "hel"},
		{name: "first bytes of each file", args: []string{"--bytes", "2", "1", "2"}, want: "hese"},
		{name: "byte limit larger than file", args: []string{"-c", "1K", "1"}, want: catText},
		{name: "zero bytes", args: []string{"-c", "0", "1"}, want: ""},
		{name: "first bytes numbered", args: []string{"-n", "-c", "8", "1"}, want: "     1\thello\n     2\two"},
		{name: "text before a binary tail prints to a terminal", args: []string{"-c", "2", "3"}, terminal: true, want: "MZ"},
		{name: "number lines on a terminal", args: []string{"-n", "1"}, terminal: true, want: "     1\thello\n     2\tworld\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.terminal {
				fakeTerminal(t)
			}
			requests := 0
			server := newCatServer(t, catContents(), &requests)

			stdout, stderr, err := runCat(t, server, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stdout != tt.want {
				t.Errorf("stdout = %q, want %q", stdout, tt.want)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestFileCatRefusesBinaryOnTerminal(t *testing.T) {
	fakeTerminal(t)
	requests := 0
	server := newCatServer(t, catContents(), &requests)

	stdout, _, err := runCat(t, server, "3")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"file 3:", `"file-3.bin"`, "looks binary", "openrelik download 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestFileCatErrors(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantErr      string
		wantStdout   string
		wantRequests bool
	}{
		{name: "not found on second ID", args: []string{"1", "404"}, wantErr: "file 404:", wantStdout: catText, wantRequests: true},
		{name: "numbered output flushed before error", args: []string{"-n", "1", "404"}, wantErr: "file 404:", wantStdout: "     1\thello\n     2\tworld\n", wantRequests: true},
		{name: "server error on second ID", args: []string{"1", "500"}, wantErr: "file 500:", wantStdout: catText, wantRequests: true},
		{name: "name without folder", args: []string{"1", "abc"}, wantErr: `invalid file "abc": give a file ID or a path`},
		{name: "folder path", args: []string{"Case A/evidence/"}, wantErr: "names a folder"},
		{name: "invalid byte count", args: []string{"-c", "lots", "1"}, wantErr: `invalid byte count "lots"`},
		{name: "zero ID", args: []string{"0"}, wantErr: "invalid file ID: 0"},
		{name: "unknown root folder", args: []string{"Case B/notes.txt"}, wantErr: `no root folder named "Case B"`},
		{name: "unknown file", args: []string{"/Case A/evidence/missing.txt"}, wantErr: `no file named "missing.txt" in /Case A/evidence/`},
		{
			name:    "duplicate file names",
			args:    []string{"1", "Case A/dup.txt"},
			wantErr: "multiple files match \"Case A/dup.txt\":\n\nID  NAME     SIZE  UPDATED\n2   dup.txt  7B",
		},
		{name: "no argument", args: nil, wantErr: "missing required argument: FILE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := newCatServer(t, catContents(), &requests)

			stdout, _, err := runCat(t, server, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if stdout != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, tt.wantStdout)
			}
			if !tt.wantRequests && requests != 0 {
				t.Errorf("made %d file requests, want none", requests)
			}
		})
	}
}

func TestLineNumberWriter(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "empty", writes: nil, want: ""},
		{name: "blank lines are numbered", writes: []string{"a\n\nb\n"}, want: "     1\ta\n     2\t\n     3\tb\n"},
		{name: "last line without newline", writes: []string{"a\nb"}, want: "     1\ta\n     2\tb"},
		{name: "line split across writes", writes: []string{"he", "llo\nwor", "ld\n"}, want: "     1\thello\n     2\tworld\n"},
		{name: "line split across files", writes: []string{"no newline", "\nnext\n"}, want: "     1\tno newline\n     2\tnext\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			w := &lineNumberWriter{w: &b}
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if err != nil || n != len(s) {
					t.Fatalf("Write(%q) = %d, %v; want %d, nil", s, n, err, len(s))
				}
			}
			if got := b.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFileCatBytesStopsDownload checks that --bytes stops the download
// instead of reading the rest of the file.
func TestFileCatBytesStopsDownload(t *testing.T) {
	const total = 256 << 20
	var sent atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/1/download" {
			http.NotFound(w, r)
			return
		}
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for sent.Load() < total {
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	stdout, _, err := runCat(t, server, "-c", "10", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "xxxxxxxxxx" {
		t.Errorf("stdout = %q, want 10 bytes", stdout)
	}
	server.Close()
	if got := sent.Load(); got >= total/4 {
		t.Errorf("server sent %d of %d bytes; the download was not stopped", got, total)
	}
}

func TestDownloadByPath(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		wantFile string
		want     string
		wantErr  string
	}{
		{name: "path", arg: "Case A/notes.txt", wantFile: "file-1.bin", want: catText},
		{name: "case-insensitive path", arg: "/case a/evidence/DISK.BIN", wantFile: "file-3.bin", want: catBinary},
		{name: "duplicate file names", arg: "Case A/dup.txt", wantErr: `multiple files match "Case A/dup.txt"`},
		{name: "unknown file", arg: "Case A/missing.txt", wantErr: `no file named "missing.txt" in Case A/`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetBaseDir(t.TempDir())
			t.Cleanup(func() { config.SetBaseDir("") })
			requests := 0
			server := newCatServer(t, catContents(), &requests)
			t.Setenv("OPENRELIK_API_KEY", "test-key")
			t.Setenv("OPENRELIK_SERVER_URL", server.URL)

			dest := t.TempDir()
			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs([]string{"download", tt.arg, dest, "-q"})
			err := root.Execute()

			entries, readErr := os.ReadDir(dest)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				if len(entries) != 0 {
					t.Errorf("wrote %d files, want none", len(entries))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dest, tt.wantFile))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("downloaded %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFileInfo(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantID  float64
		wantErr string
	}{
		{name: "ID", args: []string{"3"}, wantID: 3},
		{name: "path", args: []string{"/Case A/evidence/disk.bin"}, wantID: 3},
		{name: "duplicate file names", args: []string{"Case A/dup.txt"}, wantErr: `multiple files match "Case A/dup.txt"`},
		{name: "name without folder", args: []string{"abc"}, wantErr: `invalid file "abc"`},
		{name: "unquoted path", args: []string{"Case", "A/evidence/disk.bin"}, wantErr: `Quote FILE if it contains spaces: "Case A/evidence/disk.bin"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetBaseDir(t.TempDir())
			t.Cleanup(func() { config.SetBaseDir("") })
			requests := 0
			server := newCatServer(t, catContents(), &requests)
			t.Setenv("OPENRELIK_API_KEY", "test-key")
			t.Setenv("OPENRELIK_SERVER_URL", server.URL)

			root := NewRootCmd()
			stdout := new(bytes.Buffer)
			root.SetOut(stdout)
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(append([]string{"file", "info", "--format", "json"}, tt.args...))
			err := root.Execute()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON %q: %v", stdout, err)
			}
			if got["id"] != tt.wantID {
				t.Errorf("id = %v, want %v", got["id"], tt.wantID)
			}
		})
	}
}

func TestFileInfoView(t *testing.T) {
	created := time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("CET", 3600))
	updated := created.Add(90 * time.Second)
	str := func(s string) *string { return &s }

	t.Run("all fields", func(t *testing.T) {
		v := &FileInfoView{&openrelik.File{
			ID: 4121, DisplayName: "disk.E01", OriginalPath: str("/mnt/evidence/disk.E01"),
			Description: str("Laptop image"), Folder: openrelik.Folder{ID: 42, DisplayName: "Case A"},
			User: openrelik.User{Username: "analyst"}, Filesize: 11366,
			DataType: "file:generic", MagicMime: "application/octet-stream", MagicText: "EWF/Expert Witness/EnCase image file format",
			HashMD5: "md5hash", HashSHA1: "sha1hash", HashSHA256: "sha256hash", HashSSDeep: str("3:ssdeep"),
			CreatedAt: created, UpdatedAt: updated,
		}}
		var b strings.Builder
		if err := v.RenderHuman(&b); err != nil {
			t.Fatal(err)
		}
		age := util.FormatTimeAgo(created)
		want := `ID             4121
Name           disk.E01
Original path  /mnt/evidence/disk.E01
Description    Laptop image
Folder         42 (Case A)
Owner          analyst
Size           11.1KB (11366 bytes)
Type           file:generic
MIME           application/octet-stream
Magic          EWF/Expert Witness/EnCase image file format
MD5            md5hash
SHA1           sha1hash
SHA256         sha256hash
SSDeep         3:ssdeep
Created        2026-10-01T07:00:00Z (` + age + `)
Updated        2026-10-01T07:01:30Z (` + age + `)
`
		if b.String() != want {
			t.Errorf("output:\n%s\nwant:\n%s", b.String(), want)
		}
	})

	t.Run("missing hashes are shown as missing", func(t *testing.T) {
		v := &FileInfoView{&openrelik.File{ID: 1, DisplayName: "huge.raw", Filesize: 12, HashSHA256: "sha256hash", CreatedAt: created}}
		var b strings.Builder
		v.RenderHuman(&b)
		for _, want := range []string{"Size     12 bytes\n", "MD5      not computed\n", "SHA256   sha256hash\n", "SSDeep   not computed\n", "Updated  -\n"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("expected output to contain %q\n%s", want, b.String())
			}
		}
		for _, absent := range []string{"Original path", "Folder", "Owner", "Magic", "Deleted"} {
			if strings.Contains(b.String(), absent) {
				t.Errorf("expected no %q line for a field that isn't set\n%s", absent, b.String())
			}
		}
	})

	t.Run("deleted", func(t *testing.T) {
		v := &FileInfoView{&openrelik.File{ID: 1, IsDeleted: true, DeletedAt: &updated}}
		var b strings.Builder
		v.RenderHuman(&b)
		if !strings.Contains(b.String(), "Deleted  yes, 2026-10-01T07:01:30Z (") {
			t.Errorf("expected the deletion time\n%s", b.String())
		}
	})
}
