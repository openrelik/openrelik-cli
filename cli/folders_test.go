package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	openrelik "github.com/openrelik/openrelik-go-client"
)

func TestFolderListCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/folders/all/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"folders": [{"id": 1, "display_name": "Root 1", "uuid": "uuid1"}, {"id": 2, "display_name": "Root 2", "uuid": "uuid2"}], "page": 1, "page_size": 10, "total_count": 2}`)
			return
		}
		if r.URL.Path == "/api/v1/folders/1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `[{"id": 3, "display_name": "Sub 1", "uuid": "uuid3"}]`)
			return
		}
		if r.URL.Path == "/api/v1/folders/1/files/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `[{"id": 3, "display_name": "evidence.E01", "filesize": 2048, "data_type": "file:generic"}]`)
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

	tests := []struct {
		name           string
		args           []string
		expectedOutput []string
	}{
		{
			name: "list root folders",
			args: []string{"folder", "list"},
			expectedOutput: []string{
				"Root 1",
				"Root 2",
			},
		},
		{
			name: "list folder contents",
			args: []string{"folder", "list", "1"},
			expectedOutput: []string{
				"Sub 1/",
				"evidence.E01",
				"2.0KB",
				"file:generic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(tt.args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}

			output := buf.String()
			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("expected output to contain %q, but it was %q", expected, output)
				}
			}
		})
	}
}

func TestFolderCreateCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 100, "display_name": "New Root", "uuid": "uuid100"}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 200, "display_name": "New Sub", "uuid": "uuid200"}`)
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

	tests := []struct {
		name           string
		args           []string
		expectedOutput []string
	}{
		{
			name: "create root folder",
			args: []string{"folder", "create", "New Root"},
			expectedOutput: []string{
				`"New Root" created (ID 100)`,
			},
		},
		{
			name: "create subfolder",
			args: []string{"folder", "create", "New Sub", "--parent", "1"},
			expectedOutput: []string{
				`"New Sub" created (ID 200)`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(tt.args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}

			output := buf.String()
			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("expected output to contain %q, but it was %q", expected, output)
				}
			}
		})
	}
}

// newPaginatedFolderServer serves totalRoot root folders from /folders/all/
// honoring page and page_size, plus subfolderCount subfolders and fileCount
// files in folder 1. Subfolder and file IDs both start at 1001 so they clash,
// and update times are scrambled so sorting is exercised. Subfolder 1001 and
// file 1001 share a name and update time to exercise the kind tiebreak. Each
// root folder request is counted in requests.
func newPaginatedFolderServer(t *testing.T, totalRoot, subfolderCount, fileCount int, requests *int) *httptest.Server {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts := func(hours int) string { return base.Add(time.Duration(hours) * time.Hour).Format(time.RFC3339) }
	folderJSON := func(id int, prefix string) string {
		return fmt.Sprintf(`{"id": %d, "display_name": "%s %d", "uuid": "uuid%d", "updated_at": %q}`,
			id, prefix, id, id, ts((id*37)%101))
	}
	fileJSON := func(id int) string {
		name, hours := fmt.Sprintf("File %d", id), (id*53)%103
		if id == 1001 {
			name, hours = "Sub 1001", (id*37)%101
		}
		return fmt.Sprintf(`{"id": %d, "display_name": %q, "filesize": %d, "data_type": "file:generic", "updated_at": %q}`,
			id, name, id*1024, ts(hours))
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/folders/all/":
			*requests++
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
			if page == 0 {
				page = 1
			}
			if pageSize == 0 {
				pageSize = 40
			}
			var items []string
			for id := (page-1)*pageSize + 1; id <= min(page*pageSize, totalRoot); id++ {
				items = append(items, folderJSON(id, "Root"))
			}
			fmt.Fprintf(w, `{"folders": [%s], "page": %d, "page_size": %d, "total_count": %d}`,
				strings.Join(items, ","), page, pageSize, totalRoot)
		case "/api/v1/folders/1/folders/":
			var items []string
			for id := 1001; id < 1001+subfolderCount; id++ {
				items = append(items, folderJSON(id, "Sub"))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		case "/api/v1/folders/1/files/":
			var items []string
			for id := 1001; id < 1001+fileCount; id++ {
				items = append(items, fileJSON(id))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runFolderListServer executes the CLI with args against server and returns
// stdout, stderr and the error from Execute.
func runFolderListServer(t *testing.T, server *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"folder", "list"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// runFolderList executes the CLI with JSON output against a mock server and
// returns the decoded listing, stderr output, and root folder request count.
func runFolderList(t *testing.T, totalRoot, subfolderCount, fileCount int, args ...string) (folderContents, string, int) {
	t.Helper()
	requests := 0
	server := newPaginatedFolderServer(t, totalRoot, subfolderCount, fileCount, &requests)
	defer server.Close()

	stdout, stderr, err := runFolderListServer(t, server, append(args, "--format", "json")...)
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	var contents folderContents
	if err := json.Unmarshal([]byte(stdout), &contents); err != nil {
		t.Fatalf("output is not a folder listing: %v\n%s", err, stdout)
	}
	if contents.Folders == nil || contents.Files == nil {
		t.Fatalf("folders and files must be arrays, not null: %s", stdout)
	}
	return contents, stderr, requests
}

// entry is a kind-agnostic view of a listing row for sort assertions.
type entry struct {
	kind    string
	id      int
	name    string
	updated time.Time
}

func folderEntries(folders []openrelik.Folder) []entry {
	var out []entry
	for _, f := range folders {
		out = append(out, entry{"folder", f.ID, f.DisplayName, f.UpdatedAt})
	}
	return out
}

func fileEntries(files []openrelik.FolderFile) []entry {
	var out []entry
	for _, f := range files {
		out = append(out, entry{"file", f.ID, f.DisplayName, f.UpdatedAt})
	}
	return out
}

// assertSorted fails unless less reports no adjacent pair out of order.
func assertSorted(t *testing.T, entries []entry, less func(a, b entry) bool) {
	t.Helper()
	for i := 1; i < len(entries); i++ {
		if less(entries[i], entries[i-1]) {
			t.Fatalf("entries out of order at index %d: %+v before %+v", i, entries[i-1], entries[i])
		}
	}
}

func byName(a, b entry) bool { return strings.ToLower(a.name) < strings.ToLower(b.name) }

func byOldest(a, b entry) bool { return a.updated.Before(b.updated) }

func flip(less func(a, b entry) bool) func(a, b entry) bool {
	return func(a, b entry) bool { return less(b, a) }
}

func TestFolderListFetchesAllPages(t *testing.T) {
	contents, _, requests := runFolderList(t, 250, 0, 0)
	if len(contents.Folders) != 250 {
		t.Errorf("got %d folders, want 250", len(contents.Folders))
	}
	if len(contents.Files) != 0 {
		t.Errorf("got %d files at the root, want 0", len(contents.Files))
	}
	if requests != 3 {
		t.Errorf("got %d requests, want 3", requests)
	}
	seen := make(map[int]bool)
	for _, f := range contents.Folders {
		seen[f.ID] = true
	}
	if len(seen) != 250 {
		t.Errorf("got %d unique folders, want 250", len(seen))
	}
}

func TestFolderListSorting(t *testing.T) {
	tests := []struct {
		name string
		args []string
		less func(a, b entry) bool
	}{
		{name: "default sorts oldest first", less: byOldest},
		{name: "-r sorts newest first", args: []string{"-r"}, less: flip(byOldest)},
		{name: "--reverse sorts newest first", args: []string{"--reverse"}, less: flip(byOldest)},
		{name: "--sort name sorts by name", args: []string{"--sort", "name"}, less: byName},
		{name: "--sort name -r reverses name sort", args: []string{"--sort", "name", "-r"}, less: flip(byName)},
		{name: "--sort time is the default", args: []string{"--sort", "time"}, less: byOldest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _, _ := runFolderList(t, 90, 0, 0, tt.args...)
			if len(root.Folders) != 90 {
				t.Fatalf("got %d folders, want 90", len(root.Folders))
			}
			assertSorted(t, folderEntries(root.Folders), tt.less)

			contents, _, _ := runFolderList(t, 0, 20, 20, append([]string{"1"}, tt.args...)...)
			assertSorted(t, folderEntries(contents.Folders), tt.less)
			assertSorted(t, fileEntries(contents.Files), tt.less)
		})
	}
}

func TestFolderListLimitKeepsMostRecent(t *testing.T) {
	all, _, _ := runFolderList(t, 90, 0, 0)
	newest := make(map[int]bool)
	for _, f := range all.Folders[80:] {
		newest[f.ID] = true
	}

	for _, args := range [][]string{
		{"--limit", "10"},
		{"--limit", "10", "-r"},
		{"--limit", "10", "--sort", "name"},
	} {
		limited, _, _ := runFolderList(t, 90, 0, 0, args...)
		if len(limited.Folders) != 10 {
			t.Fatalf("%v: got %d folders, want 10", args, len(limited.Folders))
		}
		for _, f := range limited.Folders {
			if !newest[f.ID] {
				t.Errorf("%v: folder %d is not among the 10 most recently updated", args, f.ID)
			}
		}
	}

	// The default order still applies to the limited set.
	limited, _, _ := runFolderList(t, 90, 0, 0, "--limit", "10")
	for i, f := range limited.Folders {
		if want := all.Folders[80+i].ID; f.ID != want {
			t.Errorf("limited[%d] has id %d, want %d", i, f.ID, want)
		}
	}
	limited, _, _ = runFolderList(t, 90, 0, 0, "--limit", "10", "--sort", "name")
	assertSorted(t, folderEntries(limited.Folders), byName)
}

func TestFolderListContents(t *testing.T) {
	contents, _, requests := runFolderList(t, 0, 5, 7, "1")
	if len(contents.Folders) != 5 || len(contents.Files) != 7 {
		t.Errorf("got %d folders and %d files, want 5 and 7", len(contents.Folders), len(contents.Files))
	}
	if requests != 0 {
		t.Errorf("listing a folder must not fetch root folders, got %d requests", requests)
	}
}

func TestFolderListLimitAcrossKinds(t *testing.T) {
	all, _, _ := runFolderList(t, 0, 20, 20, "1")
	merged := append(folderEntries(all.Folders), fileEntries(all.Files)...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].updated.After(merged[j].updated) })
	newest := make(map[string]bool)
	for _, e := range merged[:5] {
		newest[fmt.Sprintf("%s/%d", e.kind, e.id)] = true
	}

	limited, stderr, _ := runFolderList(t, 0, 20, 20, "1", "--limit", "5")
	got := append(folderEntries(limited.Folders), fileEntries(limited.Files)...)
	if len(got) != 5 {
		t.Fatalf("got %d entries, want 5", len(got))
	}
	for _, e := range got {
		if !newest[fmt.Sprintf("%s/%d", e.kind, e.id)] {
			t.Errorf("%s %d is not among the 5 most recently updated entries", e.kind, e.id)
		}
	}
	if stderr != "" {
		t.Errorf("expected no hint with JSON output, got %q", stderr)
	}
}

func TestSortEntriesInterleavesKinds(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC) }
	folder := func(id int, name string, h int) listEntry {
		return listEntry{folder: &openrelik.Folder{ID: id, DisplayName: name, UpdatedAt: at(h)}}
	}
	file := func(id int, name string, h int) listEntry {
		return listEntry{file: &openrelik.FolderFile{ID: id, DisplayName: name, UpdatedAt: at(h)}}
	}
	label := func(entries []listEntry) string {
		var parts []string
		for _, e := range entries {
			kind := "file"
			if e.folder != nil {
				kind = "folder"
			}
			parts = append(parts, fmt.Sprintf("%s%d", kind, e.id()))
		}
		return strings.Join(parts, " ")
	}
	entries := func() []listEntry {
		return []listEntry{
			file(5, "same", 3),
			folder(9, "beta", 1),
			file(2, "Alpha", 4),
			folder(5, "same", 3),
			file(7, "gamma", 2),
		}
	}

	tests := []struct {
		name    string
		limit   int
		sortBy  string
		reverse bool
		want    string
	}{
		{name: "time", sortBy: "time", want: "folder9 file7 folder5 file5 file2"},
		{name: "time reversed", sortBy: "time", reverse: true, want: "file2 file5 folder5 file7 folder9"},
		{name: "name", sortBy: "name", want: "file2 folder9 file7 folder5 file5"},
		{name: "name reversed", sortBy: "name", reverse: true, want: "file5 folder5 file7 folder9 file2"},
		{name: "limit keeps newest", limit: 2, sortBy: "time", want: "file5 file2"},
		{name: "limit then name", limit: 3, sortBy: "name", want: "file2 folder5 file5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := label(limitAndSortEntries(entries(), tt.limit, tt.sortBy, tt.reverse))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFolderListHumanOutput(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantRows     int
		wantHint     string
		wantContains []string
		wantErr      bool
	}{
		{name: "all by default", wantRows: 90},
		{name: "limit", args: []string{"--limit", "10"}, wantRows: 10, wantHint: "Showing 10 of 90 folders."},
		{name: "limit above total", args: []string{"--limit", "500"}, wantRows: 90},
		{name: "quiet suppresses hint", args: []string{"--limit", "10", "--quiet"}},
		{
			name:         "folder contents",
			args:         []string{"1"},
			wantRows:     6,
			wantContains: []string{"SIZE", "TYPE", "Sub 1001/", "folder", "File 1002", "1002.0KB", "file:generic"},
		},
		{name: "folder contents limit", args: []string{"1", "--limit", "3"}, wantRows: 3, wantHint: "Showing 3 of 6 items."},
		{name: "negative limit", args: []string{"--limit", "-1"}, wantErr: true},
		{name: "invalid sort", args: []string{"--sort", "size"}, wantErr: true},
		{name: "-t removed", args: []string{"-t"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := newPaginatedFolderServer(t, 90, 3, 3, &requests)
			defer server.Close()

			stdout, stderr, err := runFolderListServer(t, server, tt.args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}

			rows := 0
			if stdout != "" {
				// Subtract the header row.
				rows = strings.Count(stdout, "\n") - 1
			}
			if rows != tt.wantRows {
				t.Errorf("got %d rows, want %d\n%s", rows, tt.wantRows, stdout)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(stdout, want) {
					t.Errorf("expected output to contain %q\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "Showing") {
				t.Errorf("hint must not be written to stdout: %q", stdout)
			}
			if tt.wantHint == "" && stderr != "" {
				t.Errorf("expected no hint, got %q", stderr)
			}
			if tt.wantHint != "" && !strings.Contains(stderr, tt.wantHint) {
				t.Errorf("expected stderr to contain %q, got %q", tt.wantHint, stderr)
			}
		})
	}
}

func TestFolderListJSONHasNoHint(t *testing.T) {
	_, stderr, _ := runFolderList(t, 90, 0, 0, "--limit", "10")
	if stderr != "" {
		t.Errorf("expected no hint with JSON output, got %q", stderr)
	}
}

func TestFolderListVerbose(t *testing.T) {
	requests := 0
	server := newPaginatedFolderServer(t, 0, 2, 2, &requests)
	defer server.Close()

	stdout, _, err := runFolderListServer(t, server, "1", "--format", "verbose")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	for _, want := range []string{"Folders:", "Sub 1002", "Files:", "File 1002"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected verbose output to contain %q\n%s", want, stdout)
		}
	}
}

func TestFolderListFilesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/folders/1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id": 2, "display_name": "Sub"}]`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	stdout, _, err := runFolderListServer(t, server, "1")
	if err == nil {
		t.Fatal("expected an error when listing files fails")
	}
	if stdout != "" {
		t.Errorf("expected no output on error, got %q", stdout)
	}
}

// uploadServer is a mock API that records the folders created and the files
// uploaded by 'folder upload'.
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
	root.SetArgs(append([]string{"folder", "upload"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func TestFolderUpload(t *testing.T) {
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

func TestFolderUploadErrors(t *testing.T) {
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

func TestFolderUploadJSON(t *testing.T) {
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

// newFolderTreeServer serves a fixed folder tree for path resolution tests:
//
//	/Case A/ (1)        /evidence/ (11)
//	/dup/ (2)           /logs/ (21), /shared/ (22)
//	/dup/ (3)           /shared/ (31)
//	/42/ (4)
//
// Every folder is empty of files. Requested subfolder listings are recorded.
func newFolderTreeServer(t *testing.T, listed *[]string) *httptest.Server {
	t.Helper()
	folder := func(id int, name string) string {
		return fmt.Sprintf(`{"id": %d, "display_name": %q, "user": {"username": "dev"}}`, id, name)
	}
	children := map[string][]string{
		"1": {folder(11, "evidence")},
		"2": {folder(21, "logs"), folder(22, "shared")},
		"3": {folder(31, "shared")},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.URL.Path == "/api/v1/folders/all/":
			roots := []string{folder(1, "Case A"), folder(2, "dup"), folder(3, "dup"), folder(4, "42")}
			fmt.Fprintf(w, `{"folders": [%s], "page": 1, "page_size": 100, "total_count": 4}`, strings.Join(roots, ","))
		case len(parts) == 5 && parts[4] == "folders":
			*listed = append(*listed, parts[3])
			fmt.Fprintf(w, "[%s]", strings.Join(children[parts[3]], ","))
		case len(parts) == 5 && parts[4] == "files":
			fmt.Fprint(w, "[]")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestFolderListByPath(t *testing.T) {
	tests := []struct {
		name       string
		arg        string
		wantFolder string // ID whose contents are listed
		wantErr    []string
	}{
		{name: "root name with trailing slash", arg: "Case A/", wantFolder: "1"},
		{name: "root name without slash", arg: "Case A", wantFolder: "1"},
		{name: "nested path", arg: "Case A/evidence/", wantFolder: "11"},
		{name: "leading slash", arg: "/Case A/evidence", wantFolder: "11"},
		{name: "case-insensitive fallback", arg: "case a/EVIDENCE/", wantFolder: "11"},
		{name: "digits are an ID", arg: "42", wantFolder: "42"},
		{name: "trailing slash makes digits a name", arg: "42/", wantFolder: "4"},
		{name: "duplicate parent resolved by child", arg: "dup/logs/", wantFolder: "21"},
		{
			name:    "duplicate root names",
			arg:     "dup/",
			wantErr: []string{`multiple folders match "dup/"`, "ID", "LOCATION", "\n2 ", "\n3 ", "/dup/", "folder list <ID>"},
		},
		{
			name:    "duplicate complete paths",
			arg:     "dup/shared/",
			wantErr: []string{`multiple folders match "dup/shared/"`, "\n22 ", "\n31 ", "/dup/shared/"},
		},
		{name: "missing root", arg: "nope/", wantErr: []string{`no root folder named "nope"`}},
		{name: "missing child", arg: "Case A/nope/", wantErr: []string{`no folder named "nope" in /Case A/`}},
		{name: "empty path", arg: "/", wantErr: []string{`invalid folder path "/"`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var listed []string
			server := newFolderTreeServer(t, &listed)
			defer server.Close()

			_, _, err := runFolderListServer(t, server, tt.arg)
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("expected error to contain %q, got:\n%s", want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if len(listed) == 0 || listed[len(listed)-1] != tt.wantFolder {
				t.Errorf("listed folders %v, want the last to be %s", listed, tt.wantFolder)
			}
		})
	}
}

func TestFolderCreateWithParentPath(t *testing.T) {
	tests := []struct {
		name           string
		parent         string
		wantSubfolders []string
		wantErr        string
	}{
		{name: "parent ID", parent: "42", wantSubfolders: []string{"42/New"}},
		{name: "parent path", parent: "Case A/evidence/", wantSubfolders: []string{"43/New"}},
		{name: "unknown parent path", parent: "nope/", wantErr: `no root folder named "nope"`},
		{name: "zero parent ID", parent: "0", wantErr: "invalid folder ID: 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newUploadServer(t)
			defer server.Close()
			t.Setenv("OPENRELIK_API_KEY", "test-key")
			t.Setenv("OPENRELIK_SERVER_URL", server.URL)

			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs([]string{"folder", "create", "New", "--parent", tt.parent})
			err := root.Execute()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				if len(server.subfolders)+len(server.roots) > 0 {
					t.Errorf("nothing should be created on error, got %q %q", server.roots, server.subfolders)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if strings.Join(server.subfolders, ",") != strings.Join(tt.wantSubfolders, ",") {
				t.Errorf("created subfolders %q, want %q", server.subfolders, tt.wantSubfolders)
			}
		})
	}
}

func TestFolderListEmptyPrintsNothing(t *testing.T) {
	var listed []string
	server := newFolderTreeServer(t, &listed)
	defer server.Close()

	// Folder "42/" (ID 4) has no subfolders or files.
	stdout, stderr, err := runFolderListServer(t, server, "42/")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("expected no output for an empty folder, got stdout %q, stderr %q", stdout, stderr)
	}

	// JSON still returns the (empty) object.
	stdout, _, err = runFolderListServer(t, server, "42/", "--format", "json")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if strings.TrimSpace(stdout) != `{
  "folders": [],
  "files": []
}` {
		t.Errorf("unexpected JSON for an empty folder: %q", stdout)
	}
}
