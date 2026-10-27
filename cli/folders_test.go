package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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
			name: "list subfolders",
			args: []string{"folder", "list", "1"},
			expectedOutput: []string{
				"Sub 1",
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
// honoring page and page_size, and subfolderCount subfolders of folder 1.
// Folder update times are scrambled relative to their IDs so sorting is
// exercised. Each root folder request is counted in requests.
func newPaginatedFolderServer(t *testing.T, totalRoot, subfolderCount int, requests *int) *httptest.Server {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	folderJSON := func(id int, prefix string) string {
		updated := base.Add(time.Duration((id*37)%101) * time.Hour)
		return fmt.Sprintf(`{"id": %d, "display_name": "%s %d", "uuid": "uuid%d", "updated_at": %q}`,
			id, prefix, id, id, updated.Format(time.RFC3339))
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
			for id := 1; id <= subfolderCount; id++ {
				items = append(items, folderJSON(1000+id, "Sub"))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runFolderList executes the CLI against a mock server and returns the
// decoded JSON folders, stderr output, and root folder request count.
func runFolderList(t *testing.T, totalRoot, subfolderCount int, args ...string) ([]openrelik.Folder, string, int) {
	t.Helper()
	requests := 0
	server := newPaginatedFolderServer(t, totalRoot, subfolderCount, &requests)
	defer server.Close()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append(append([]string{"folder", "list"}, args...), "--format", "json"))
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	var folders []openrelik.Folder
	if err := json.Unmarshal(stdout.Bytes(), &folders); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, stdout.String())
	}
	return folders, stderr.String(), requests
}

// assertSorted fails unless less reports no adjacent pair out of order.
func assertSorted(t *testing.T, folders []openrelik.Folder, less func(a, b openrelik.Folder) bool) {
	t.Helper()
	for i := 1; i < len(folders); i++ {
		if less(folders[i], folders[i-1]) {
			t.Fatalf("folders out of order at index %d: %+v before %+v", i, folders[i-1], folders[i])
		}
	}
}

func byName(a, b openrelik.Folder) bool {
	return strings.ToLower(a.DisplayName) < strings.ToLower(b.DisplayName)
}

func byOldest(a, b openrelik.Folder) bool { return a.UpdatedAt.Before(b.UpdatedAt) }

func TestFolderListFetchesAllPages(t *testing.T) {
	folders, _, requests := runFolderList(t, 250, 0)
	if len(folders) != 250 {
		t.Errorf("got %d folders, want 250", len(folders))
	}
	if requests != 3 {
		t.Errorf("got %d requests, want 3", requests)
	}
	seen := make(map[int]bool)
	for _, f := range folders {
		seen[f.ID] = true
	}
	if len(seen) != 250 {
		t.Errorf("got %d unique folders, want 250", len(seen))
	}
}

func TestFolderListSorting(t *testing.T) {
	flip := func(less func(a, b openrelik.Folder) bool) func(a, b openrelik.Folder) bool {
		return func(a, b openrelik.Folder) bool { return less(b, a) }
	}
	tests := []struct {
		name string
		args []string
		less func(a, b openrelik.Folder) bool
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
			folders, _, _ := runFolderList(t, 90, 0, tt.args...)
			if len(folders) != 90 {
				t.Fatalf("got %d folders, want 90", len(folders))
			}
			assertSorted(t, folders, tt.less)
		})
	}
}

func TestFolderListLimitKeepsMostRecent(t *testing.T) {
	all, _, _ := runFolderList(t, 90, 0)
	newest := make(map[int]bool)
	for _, f := range all[80:] {
		newest[f.ID] = true
	}

	for _, args := range [][]string{
		{"--limit", "10"},
		{"--limit", "10", "-r"},
		{"--limit", "10", "--sort", "name"},
	} {
		limited, _, _ := runFolderList(t, 90, 0, args...)
		if len(limited) != 10 {
			t.Fatalf("%v: got %d folders, want 10", args, len(limited))
		}
		for _, f := range limited {
			if !newest[f.ID] {
				t.Errorf("%v: folder %d is not among the 10 most recently updated", args, f.ID)
			}
		}
	}

	// The default order still applies to the limited set.
	limited, _, _ := runFolderList(t, 90, 0, "--limit", "10")
	for i, f := range limited {
		if want := all[80+i].ID; f.ID != want {
			t.Errorf("limited[%d] has id %d, want %d", i, f.ID, want)
		}
	}
	limited, _, _ = runFolderList(t, 90, 0, "--limit", "10", "--sort", "name")
	assertSorted(t, limited, byName)
}

func TestFolderListSubfolders(t *testing.T) {
	folders, _, _ := runFolderList(t, 0, 5, "1")
	if len(folders) != 5 {
		t.Errorf("got %d subfolders, want 5", len(folders))
	}
	assertSorted(t, folders, byOldest)

	folders, _, _ = runFolderList(t, 0, 5, "1", "--limit", "3")
	if len(folders) != 3 {
		t.Errorf("got %d subfolders, want 3", len(folders))
	}
}

func TestFolderListHumanOutput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantRows int
		wantHint string
		wantErr  bool
	}{
		{name: "all by default", args: []string{"folder", "list"}, wantRows: 90},
		{name: "limit", args: []string{"folder", "list", "--limit", "10"}, wantRows: 10, wantHint: "Showing 10 of 90 folders."},
		{name: "limit above total", args: []string{"folder", "list", "--limit", "500"}, wantRows: 90},
		{name: "quiet suppresses hint", args: []string{"folder", "list", "--limit", "10", "--quiet"}},
		{name: "negative limit", args: []string{"folder", "list", "--limit", "-1"}, wantErr: true},
		{name: "invalid sort", args: []string{"folder", "list", "--sort", "size"}, wantErr: true},
		{name: "-t removed", args: []string{"folder", "list", "-t"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := newPaginatedFolderServer(t, 90, 0, &requests)
			defer server.Close()
			t.Setenv("OPENRELIK_API_KEY", "test-key")
			t.Setenv("OPENRELIK_SERVER_URL", server.URL)

			root := NewRootCmd()
			stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
			root.SetOut(stdout)
			root.SetErr(stderr)
			root.SetArgs(tt.args)

			err := root.Execute()
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
			if stdout.Len() > 0 {
				// Subtract the header row.
				rows = strings.Count(stdout.String(), "\n") - 1
			}
			if rows != tt.wantRows {
				t.Errorf("got %d rows, want %d\n%s", rows, tt.wantRows, stdout.String())
			}
			if strings.Contains(stdout.String(), "Showing") {
				t.Errorf("hint must not be written to stdout: %q", stdout.String())
			}
			if tt.wantHint == "" && stderr.Len() > 0 {
				t.Errorf("expected no hint, got %q", stderr.String())
			}
			if tt.wantHint != "" && !strings.Contains(stderr.String(), tt.wantHint) {
				t.Errorf("expected stderr to contain %q, got %q", tt.wantHint, stderr.String())
			}
		})
	}
}

func TestFolderListJSONHasNoHint(t *testing.T) {
	_, stderr, _ := runFolderList(t, 90, 0, "--limit", "10")
	if stderr != "" {
		t.Errorf("expected no hint with JSON output, got %q", stderr)
	}
}
