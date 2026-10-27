package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// treeServerFolder is a folder in the mock tree served by newTreeServer.
type treeServerFolder struct {
	name     string
	hours    int // update time, in hours after a base time
	children []int
	files    []treeServerFile
}

type treeServerFile struct {
	id    int
	name  string
	hours int
}

// treeServerFolders is the mock tree:
//
//	case/ (1)
//	├── beta/ (11)
//	│   └── deep/ (111)
//	│       └── x.log (1003)
//	├── alpha.txt (1001)
//	└── Zed.bin (1002)
//	empty/ (2)
var treeServerFolders = map[int]treeServerFolder{
	1:   {name: "case", hours: 1, children: []int{11}, files: []treeServerFile{{1001, "alpha.txt", 5}, {1002, "Zed.bin", 2}}},
	11:  {name: "beta", hours: 3, children: []int{111}},
	111: {name: "deep", hours: 4, files: []treeServerFile{{1003, "x.log", 6}}},
	2:   {name: "empty", hours: 0},
}

// newTreeServer serves treeServerFolders. Requests for folder files are
// counted in fileRequests; failFiles makes them fail.
func newTreeServer(t *testing.T, failFiles bool, fileRequests *int) *httptest.Server {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts := func(h int) string { return base.Add(time.Duration(h) * time.Hour).Format(time.RFC3339) }
	folderJSON := func(id int) string {
		f := treeServerFolders[id]
		return fmt.Sprintf(`{"id": %d, "display_name": %q, "updated_at": %q}`, id, f.name, ts(f.hours))
	}
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // api v1 folders ...
		if r.URL.Path == "/api/v1/folders/all/" {
			fmt.Fprintf(w, `{"folders": [%s,%s], "page": 1, "page_size": 100, "total_count": 2}`, folderJSON(1), folderJSON(2))
			return
		}
		var id int
		if len(parts) < 4 || parts[2] != "folders" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Sscan(parts[3], &id)
		f, ok := treeServerFolders[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch {
		case len(parts) == 4:
			fmt.Fprint(w, folderJSON(id))
		case parts[4] == "folders":
			var items []string
			for _, c := range f.children {
				items = append(items, folderJSON(c))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		case parts[4] == "files":
			mu.Lock()
			*fileRequests++
			mu.Unlock()
			if failFiles {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var items []string
			for _, file := range f.files {
				items = append(items, fmt.Sprintf(`{"id": %d, "display_name": %q, "updated_at": %q}`, file.id, file.name, ts(file.hours)))
			}
			fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runTree runs 'folder <args>' against server and returns stdout.
func runTree(t *testing.T, server *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	root := NewRootCmd()
	stdout := new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(append([]string{"folder"}, args...))
	err := root.Execute()
	return stdout.String(), err
}

func TestFolderTree(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		want             string
		wantFileRequests int
	}{
		{
			name: "folder sorted by name",
			args: []string{"1"},
			want: `[1]  case/
├── [1001]  alpha.txt
├── [11]  beta/
│   └── [111]  deep/
│       └── [1003]  x.log
└── [1002]  Zed.bin

2 folders, 3 files
`,
			wantFileRequests: 3,
		},
		{
			name: "folder by path, sorted by time",
			args: []string{"case/", "--sort", "time"},
			want: `[1]  case/
├── [1002]  Zed.bin
├── [11]  beta/
│   └── [111]  deep/
│       └── [1003]  x.log
└── [1001]  alpha.txt

2 folders, 3 files
`,
			wantFileRequests: 3,
		},
		{
			name: "reverse",
			args: []string{"1", "-r"},
			want: `[1]  case/
├── [1002]  Zed.bin
├── [11]  beta/
│   └── [111]  deep/
│       └── [1003]  x.log
└── [1001]  alpha.txt

2 folders, 3 files
`,
			wantFileRequests: 3,
		},
		{
			name: "level limits depth",
			args: []string{"1", "--level", "1"},
			want: `[1]  case/
├── [1001]  alpha.txt
├── [11]  beta/
└── [1002]  Zed.bin

1 folder, 2 files
`,
			wantFileRequests: 1,
		},
		{
			name: "folders only skips files",
			args: []string{"1", "--folders-only"},
			want: `[1]  case/
└── [11]  beta/
    └── [111]  deep/

2 folders
`,
		},
		{
			name: "all root folders",
			args: []string{},
			want: `.
├── [1]  case/
│   ├── [1001]  alpha.txt
│   ├── [11]  beta/
│   │   └── [111]  deep/
│   │       └── [1003]  x.log
│   └── [1002]  Zed.bin
└── [2]  empty/

4 folders, 3 files
`,
			wantFileRequests: 4,
		},
		{
			name: "all root folders, level 1",
			args: []string{"--level", "1"},
			want: `.
├── [1]  case/
└── [2]  empty/

2 folders, 0 files
`,
		},
		{
			name: "empty folder",
			args: []string{"2"},
			want: `[2]  empty/

0 folders, 0 files
`,
			wantFileRequests: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileRequests := 0
			server := newTreeServer(t, false, &fileRequests)
			defer server.Close()

			got, err := runTree(t, server, append([]string{"tree"}, tt.args...)...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
			if fileRequests != tt.wantFileRequests {
				t.Errorf("got %d file requests, want %d", fileRequests, tt.wantFileRequests)
			}
		})
	}
}

func TestFolderTreeJSON(t *testing.T) {
	fileRequests := 0
	server := newTreeServer(t, false, &fileRequests)
	defer server.Close()

	type node struct {
		ID          int    `json:"id"`
		DisplayName string `json:"display_name"`
		Folders     []node `json:"folders"`
		Files       []struct {
			ID int `json:"id"`
		} `json:"files"`
	}

	out, err := runTree(t, server, "tree", "1", "--level", "1", "--format", "json")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	var root node
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if root.ID != 1 || len(root.Folders) != 1 || len(root.Files) != 2 {
		t.Fatalf("unexpected root: %+v", root)
	}
	// beta/ is beyond --level, so its contents were not fetched.
	if !strings.Contains(out, `"folders": null`) || root.Folders[0].Folders != nil {
		t.Errorf("unfetched folder contents should be null:\n%s", out)
	}

	// Without a folder, the virtual root has only folders and files arrays.
	out, err = runTree(t, server, "tree", "--level", "1", "--format", "json")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(top) != 2 || string(top["files"]) != "[]" {
		t.Errorf("unexpected top level: %s", out)
	}
}

func TestFolderTreeErrors(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		failFiles bool
		wantErr   string
	}{
		{name: "files request fails", args: []string{"1"}, failFiles: true, wantErr: "failed to list folder 1"},
		{name: "negative level", args: []string{"1", "--level", "-1"}, wantErr: "invalid level"},
		{name: "invalid sort", args: []string{"1", "--sort", "size"}, wantErr: "invalid sort"},
		{name: "unknown path", args: []string{"nope/"}, wantErr: `no root folder named "nope"`},
		{name: "no limit flag", args: []string{"1", "--limit", "3"}, wantErr: "unknown flag: --limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileRequests := 0
			server := newTreeServer(t, tt.failFiles, &fileRequests)
			defer server.Close()

			out, err := runTree(t, server, append([]string{"tree"}, tt.args...)...)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if out != "" {
				t.Errorf("expected no output on error, got %q", out)
			}
		})
	}
}
