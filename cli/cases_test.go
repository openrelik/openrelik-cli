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

	"github.com/openrelik/openrelik-cli/config"
	openrelik "github.com/openrelik/openrelik-go-client"
)

// caseServer mimics the case folder endpoints. Case case-123 has the case
// folder 42 "ACME", owned by alice, holding the folder 43 "evidence" with the
// file 70 and the workflow 5. Folder 50, its file 71 and its workflow 6 are
// outside the case.
type caseServer struct {
	*httptest.Server
	// old makes it a server without case folders.
	old bool
	// hidden makes case-123 a case the user can't see: lookups return 404
	// and creating it 409.
	hidden bool
	// conflictOnce makes the next case folder creation fail with 409, as
	// if someone created the case first.
	conflictOnce bool

	mu       sync.Mutex
	caseRefs map[int]string // folder ID -> case reference
	created  []string       // "<parent>/<name>[ case=<ref>]" for each folder created
	uploads  map[string]string
}

func newCaseServer(t *testing.T) *caseServer {
	t.Helper()
	s := &caseServer{caseRefs: map[int]string{42: "case-123"}, uploads: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *caseServer) folderJSON(id int) string {
	names := map[int]string{42: "ACME", 43: "evidence", 50: "Elsewhere", 900: "new"}
	owner := `{"id": 8, "username": "alice"}`
	if id == 50 || id == 900 {
		owner = `{"id": 7, "username": "me"}`
	}
	caseRef := ""
	if ref := s.caseRefs[id]; ref != "" && !s.old {
		caseRef = fmt.Sprintf(`, "case_ref": %q`, ref)
	}
	parent := ""
	if id == 43 {
		parent = `, "parent": ` + s.folderJSON(42)
	}
	workflows := map[int]string{43: `[{"id": 5}]`, 50: `[{"id": 6}]`}[id]
	if workflows == "" {
		workflows = "[]"
	}
	return fmt.Sprintf(`{"id": %d, "display_name": %q, "user": %s, "workflows": %s%s%s}`, id, names[id], owner, workflows, caseRef, parent)
}

// normalize does what the server does to case references, for the
// references in these tests.
func normalize(ref string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(ref)), " ", "-")
}

func (s *caseServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	notFound := func(detail string) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"detail": %q}`, detail)
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/api/v1/users/me/":
		fmt.Fprint(w, `{"id": 7, "username": "me"}`)
	case r.Method == http.MethodGet && path == "/api/v1/folders/all/":
		fmt.Fprintf(w, `{"folders": [%s, %s], "page": 1, "page_size": 100, "total_count": 2}`, s.folderJSON(42), s.folderJSON(50))
	case strings.HasPrefix(path, "/api/v1/folders/case/"):
		ref := normalize(strings.TrimPrefix(path, "/api/v1/folders/case/"))
		switch {
		case s.old:
			notFound("Not Found")
		case strings.Contains(ref, "#"):
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"detail": "case reference can only contain letters a-z, digits, '-', '_', '.' and '/'"}`)
		default:
			for id, r := range s.caseRefs {
				if r == ref && !s.hidden {
					fmt.Fprint(w, s.folderJSON(id))
					return
				}
			}
			notFound("Case not found.")
		}
	case r.Method == http.MethodGet && (path == "/api/v1/folders/42" || path == "/api/v1/folders/43" || path == "/api/v1/folders/50"):
		var id int
		fmt.Sscanf(path, "/api/v1/folders/%d", &id)
		fmt.Fprint(w, s.folderJSON(id))
	case r.Method == http.MethodGet && path == "/api/v1/folders/42/folders/":
		fmt.Fprintf(w, `[%s]`, s.folderJSON(43))
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/folders/"):
		fmt.Fprint(w, `[]`)
	case r.Method == http.MethodGet && path == "/api/v1/folders/43/files/":
		fmt.Fprint(w, `[{"id": 70, "display_name": "disk.img"}]`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/files/"):
		fmt.Fprint(w, `[]`)
	case r.Method == http.MethodGet && path == "/api/v1/files/70":
		fmt.Fprint(w, `{"id": 70, "display_name": "disk.img", "folder": {"id": 43}}`)
	case r.Method == http.MethodGet && path == "/api/v1/files/71":
		fmt.Fprint(w, `{"id": 71, "display_name": "other.img", "folder": {"id": 50}}`)
	case r.Method == http.MethodGet && (path == "/api/v1/workflows/5" || path == "/api/v1/workflows/6"):
		var id int
		fmt.Sscanf(path, "/api/v1/workflows/%d", &id)
		fmt.Fprintf(w, `{"id": %d, "display_name": "workflow %d", "folder": {"id": %d}}`, id, id, map[int]int{5: 43, 6: 50}[id])
	case r.Method == http.MethodGet && path == "/api/v1/files/901":
		// An upload the server hasn't hashed yet.
		fmt.Fprint(w, `{"id": 901}`)
	case r.Method == http.MethodPost && path == "/api/v1/files/upload":
		name := r.URL.Query().Get("resumableFilename")
		s.uploads[name] = r.URL.Query().Get("folder_id")
		fmt.Fprintf(w, `{"id": 901, "display_name": %q}`, name)
	case r.Method == http.MethodPost && path == "/api/v1/folders/":
		var body struct {
			DisplayName string `json:"display_name"`
			CaseRef     string `json:"case_ref"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if s.hidden || s.conflictOnce {
			s.conflictOnce = false
			w.WriteHeader(http.StatusConflict)
			fmt.Fprintf(w, `{"detail": "Case %s already exists. Ask your OpenRelik admin for access."}`, body.CaseRef)
			return
		}
		entry := "/" + body.DisplayName
		if body.CaseRef != "" && !s.old {
			entry += " case=" + body.CaseRef
			s.caseRefs[900] = normalize(body.CaseRef)
		}
		s.created = append(s.created, entry)
		caseRef := ""
		if body.CaseRef != "" && !s.old {
			caseRef = fmt.Sprintf(`, "case_ref": %q`, normalize(body.CaseRef))
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id": 900, "display_name": %q, "user": {"id": 7, "username": "me"}%s}`, body.DisplayName, caseRef)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/folders/"):
		var body struct {
			DisplayName string `json:"display_name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		parent := strings.Split(path, "/")[4]
		s.created = append(s.created, parent+"/"+body.DisplayName)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id": 901, "display_name": %q}`, body.DisplayName)
	case path == "/api/v1/folders/42/case" || path == "/api/v1/folders/43/case":
		if s.old {
			notFound("Not Found")
			return
		}
		if path == "/api/v1/folders/43/case" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"detail": "only root folders can be case folders"}`)
			return
		}
		if r.Method == http.MethodDelete {
			delete(s.caseRefs, 42)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body struct {
			CaseRef string `json:"case_ref"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		s.caseRefs[42] = normalize(body.CaseRef)
		fmt.Fprint(w, s.folderJSON(42))
	default:
		notFound("Not Found")
	}
}

func runCase(t *testing.T, s *caseServer, args ...string) (string, string, error) {
	t.Helper()
	config.SetBaseDir(t.TempDir())
	t.Cleanup(func() { config.SetBaseDir("") })
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", s.URL)
	if err := config.SaveWorkersCache(s.URL, []openrelik.Worker{{TaskName: "openrelik-worker-strings.tasks.strings", DisplayName: "Strings"}}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// checkExit fails the test unless err gives the exit code want and, if
// want isn't 0, contains msg.
func checkExit(t *testing.T, err error, want int, msg string) {
	t.Helper()
	got := 0
	if err != nil {
		got = exitCode(err)
	}
	if got != want {
		t.Fatalf("exit code = %d, want %d (err: %v)", got, want, err)
	}
	if err != nil && !strings.Contains(err.Error(), msg) {
		t.Errorf("error %q doesn't contain %q", err, msg)
	}
}

func TestCaseCreate(t *testing.T) {
	t.Run("new case", func(t *testing.T) {
		s := newCaseServer(t)
		stdout, _, err := runCase(t, s, "case", "create", "Case 9", "Breach")
		checkExit(t, err, 0, "")
		if want := `Case folder "Breach" of case case-9 created (ID 900)`; !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
		if strings.Join(s.created, ",") != "/Breach case=Case 9" {
			t.Errorf("created %q", s.created)
		}
	})

	t.Run("name defaults to the reference", func(t *testing.T) {
		s := newCaseServer(t)
		_, _, err := runCase(t, s, "case", "create", "case-9")
		checkExit(t, err, 0, "")
		if strings.Join(s.created, ",") != "/case-9 case=case-9" {
			t.Errorf("created %q", s.created)
		}
	})

	t.Run("existing case is printed", func(t *testing.T) {
		s := newCaseServer(t)
		stdout, stderr, err := runCase(t, s, "case", "create", "CASE 123", "Other name")
		checkExit(t, err, 0, "")
		if len(s.created) > 0 {
			t.Errorf("created %q, want nothing", s.created)
		}
		for _, want := range []string{
			`Case case-123 already exists: folder 42 "ACME", owned by alice.`,
			`It keeps its name "ACME"; it wasn't renamed to "Other name".`,
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
		}
		if want := `Case folder "ACME" of case case-123 already exists (ID 42)`; !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	})

	t.Run("JSON says whether it was created", func(t *testing.T) {
		s := newCaseServer(t)
		stdout, _, err := runCase(t, s, "case", "create", "case-123", "--format", "json")
		checkExit(t, err, 0, "")
		var out struct {
			ID      int    `json:"id"`
			CaseRef string `json:"case_ref"`
			Created bool   `json:"created"`
		}
		if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.ID != 42 || out.CaseRef != "case-123" || out.Created {
			t.Errorf("unexpected JSON (%v): %s", err, stdout)
		}
	})

	t.Run("created by someone else meanwhile", func(t *testing.T) {
		s := newCaseServer(t)
		s.caseRefs = map[int]string{}
		s.conflictOnce = true
		// The second lookup finds the folder the other request created.
		orig := s.Config.Handler
		s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/" {
				s.mu.Lock()
				s.caseRefs[42] = "case-123"
				s.mu.Unlock()
			}
			orig.ServeHTTP(w, r)
		})
		stdout, _, err := runCase(t, s, "case", "create", "case-123")
		checkExit(t, err, 0, "")
		if !strings.Contains(stdout, "already exists (ID 42)") {
			t.Errorf("stdout = %q", stdout)
		}
	})

	t.Run("case the user can't see", func(t *testing.T) {
		s := newCaseServer(t)
		s.hidden = true
		_, _, err := runCase(t, s, "case", "create", "case-123")
		checkExit(t, err, exitError, "Ask your OpenRelik admin for access")
	})

	t.Run("older server", func(t *testing.T) {
		s := newCaseServer(t)
		s.old = true
		_, _, err := runCase(t, s, "case", "create", "case-9")
		checkExit(t, err, exitError, "this server doesn't support case folders")
		if len(s.created) > 0 {
			t.Errorf("created %q on an older server", s.created)
		}
	})
}

func TestFolderListCase(t *testing.T) {
	tests := []struct {
		name     string
		old      bool
		args     []string
		exit     int
		contains string
	}{
		{"case folder", false, []string{"--case", "case-123"}, 0, "evidence/"},
		{"path in the case", false, []string{"evidence/", "--case", "case-123"}, 0, "disk.img"},
		{"ID in the case", false, []string{"43", "--case", "case-123"}, 0, "disk.img"},
		{"ID outside the case", false, []string{"50", "--case", "case-123"}, exitUsage, `folder 50 is not in case case-123 (folder 42 "ACME")`},
		{"path outside the case", false, []string{"Elsewhere/", "--case", "case-123"}, exitNotFound, `no folder named "Elsewhere" in case case-123`},
		{"missing case", false, []string{"--case", "case-9"}, exitNotFound, "case case-9 not found, or you don't have access to it"},
		{"invalid reference", false, []string{"--case", "bad#ref"}, exitUsage, "invalid case reference"},
		{"refused by the client", false, []string{"--case", "ir//007"}, exitUsage, `invalid case reference "ir//007"`},
		{"with --shared", false, []string{"--shared", "--case", "case-123"}, exitUsage, "can't be used with --case"},
		{"older server", true, []string{"--case", "case-123"}, exitError, "this server doesn't support case folders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCaseServer(t)
			s.old = tt.old
			stdout, _, err := runCase(t, s, append([]string{"folder", "list"}, tt.args...)...)
			checkExit(t, err, tt.exit, tt.contains)
			if tt.exit == 0 && !strings.Contains(stdout, tt.contains) {
				t.Errorf("stdout = %q, want %q", stdout, tt.contains)
			}
		})
	}
}

func TestFolderListCaseColumn(t *testing.T) {
	s := newCaseServer(t)
	stdout, stderr, err := runCase(t, s, "folder", "list", "--sort", "name")
	checkExit(t, err, 0, "")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "CASE") ||
		!strings.Contains(lines[1], "case-123") || !strings.Contains(lines[2], "-  ") {
		t.Errorf("unexpected listing:\n%s%s", stdout, stderr)
	}

	// Subfolders can't be case folders, so their listings have no CASE column.
	stdout, _, err = runCase(t, s, "folder", "list", "42")
	checkExit(t, err, 0, "")
	if strings.Contains(stdout, "CASE") {
		t.Errorf("unexpected CASE column:\n%s", stdout)
	}
}

func TestFolderCreateInCase(t *testing.T) {
	t.Run("existing folder of another owner is used", func(t *testing.T) {
		s := newCaseServer(t)
		stdout, stderr, err := runCase(t, s, "folder", "create", "evidence", "--case", "case-123", "--use-existing")
		checkExit(t, err, 0, "")
		if len(s.created) > 0 || !strings.Contains(stdout, `Folder "evidence" already exists (ID 43)`) {
			t.Errorf("created %q, stdout %q", s.created, stdout)
		}
		if !strings.Contains(stderr, `Using case folder 42 "ACME", owned by alice.`) {
			t.Errorf("stderr = %q", stderr)
		}
	})

	t.Run("new folder in the case folder", func(t *testing.T) {
		s := newCaseServer(t)
		_, _, err := runCase(t, s, "folder", "create", "notes", "--case", "case-123")
		checkExit(t, err, 0, "")
		if strings.Join(s.created, ",") != "42/notes" {
			t.Errorf("created %q", s.created)
		}
	})

	t.Run("parent is a path in the case", func(t *testing.T) {
		s := newCaseServer(t)
		_, _, err := runCase(t, s, "folder", "create", "logs", "--case", "case-123", "--parent", "evidence")
		checkExit(t, err, 0, "")
		if strings.Join(s.created, ",") != "43/logs" {
			t.Errorf("created %q", s.created)
		}
	})

	t.Run("parent outside the case", func(t *testing.T) {
		s := newCaseServer(t)
		_, _, err := runCase(t, s, "folder", "create", "logs", "--case", "case-123", "--parent", "50")
		checkExit(t, err, exitUsage, "folder 50 is not in case case-123")
		if len(s.created) > 0 {
			t.Errorf("created %q", s.created)
		}
	})
}

func TestCaseSetClear(t *testing.T) {
	tests := []struct {
		name   string
		old    bool
		args   []string
		exit   int
		output string
	}{
		{"set", false, []string{"set", "42", "Case 7"}, 0, `Folder 42 "ACME" is now the case folder of case case-7, instead of case case-123`},
		{"set the same", false, []string{"set", "42", "CASE-123"}, 0, `Folder 42 "ACME" is the case folder of case case-123`},
		{"clear", false, []string{"clear", "42"}, 0, `Folder 42 "ACME" is no longer the case folder of case case-123`},
		{"subfolder", false, []string{"set", "43", "case-7"}, exitUsage, "only root folders can be case folders"},
		{"no reference", false, []string{"set", "42"}, exitUsage, "missing required argument: REF"},
		{"older server", true, []string{"clear", "42"}, exitError, "this server doesn't support case folders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCaseServer(t)
			s.old = tt.old
			stdout, _, err := runCase(t, s, append([]string{"case"}, tt.args...)...)
			checkExit(t, err, tt.exit, tt.output)
			if tt.exit == 0 && !strings.Contains(stdout, tt.output) {
				t.Errorf("stdout = %q, want %q", stdout, tt.output)
			}
		})
	}
}

func TestCaseList(t *testing.T) {
	t.Run("cases", func(t *testing.T) {
		s := newCaseServer(t)
		s.caseRefs[50] = "case-0"
		stdout, _, err := runCase(t, s, "case", "list")
		checkExit(t, err, 0, "")
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "CASE") ||
			!strings.HasPrefix(lines[1], "case-0 ") || !strings.Contains(lines[1], "Elsewhere/") ||
			!strings.HasPrefix(lines[2], "case-123 ") || !strings.Contains(lines[2], "alice") {
			t.Errorf("stdout = %q", stdout)
		}
	})

	t.Run("JSON", func(t *testing.T) {
		s := newCaseServer(t)
		stdout, _, err := runCase(t, s, "case", "list", "--format", "json")
		checkExit(t, err, 0, "")
		var out []struct {
			ID      int    `json:"id"`
			CaseRef string `json:"case_ref"`
		}
		if err := json.Unmarshal([]byte(stdout), &out); err != nil || len(out) != 1 || out[0].ID != 42 || out[0].CaseRef != "case-123" {
			t.Errorf("unexpected JSON (%v): %s", err, stdout)
		}
	})

	t.Run("no cases", func(t *testing.T) {
		s := newCaseServer(t)
		s.caseRefs = map[int]string{}
		stdout, _, err := runCase(t, s, "case", "list", "--format", "json")
		checkExit(t, err, 0, "")
		if strings.TrimSpace(stdout) != "[]" {
			t.Errorf("stdout = %q, want []", stdout)
		}
	})

	t.Run("older server", func(t *testing.T) {
		s := newCaseServer(t)
		s.old = true
		_, _, err := runCase(t, s, "case", "list")
		checkExit(t, err, exitError, "this server doesn't support case folders")
	})
}

func TestUploadCase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		args     []string
		exit     int
		folderID string
		msg      string
	}{
		{"into the case folder", []string{file, "--case", "case-123"}, 0, "42", ""},
		{"into a folder in the case", []string{file, "evidence/", "--case", "case-123"}, 0, "43", ""},
		{"folder outside the case", []string{file, "50", "--case", "case-123"}, exitUsage, "", "folder 50 is not in case case-123"},
		{"new root folder", []string{file, "/", "--case", "case-123"}, exitUsage, "", `FOLDER can't be "/"`},
		{"with --name", []string{file, "--case", "case-123", "--name", "x"}, exitUsage, "", "can't be used with --name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCaseServer(t)
			_, _, err := runCase(t, s, append([]string{"upload", "--force"}, tt.args...)...)
			checkExit(t, err, tt.exit, tt.msg)
			if got := s.uploads["a.txt"]; got != tt.folderID {
				t.Errorf("uploaded to folder %q, want %q", got, tt.folderID)
			}
			if len(s.created) > 0 {
				t.Errorf("created %q", s.created)
			}
		})
	}
}

func TestRunCaseChecksInputs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		msg  string
	}{
		{"file outside the case", []string{"71"}, `file 71 is not in case case-123 (folder 42 "ACME")`},
		{"path outside the case", []string{"Elsewhere/other.img"}, `no folder named "Elsewhere" in case case-123`},
		{"with an upload folder", []string{"--upload-folder-id", "5", "70"}, "can't be used with --upload-folder-id"},
		{"missing file in the case", []string{"evidence/missing.bin"}, `no file named "missing.bin" in /ACME/evidence/ of case case-123`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCaseServer(t)
			args := append([]string{"run", "--download", "none", "--case", "case-123", "strings"}, tt.args...)
			_, _, err := runCase(t, s, args...)
			if err == nil || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("err = %v, want it to contain %q", err, tt.msg)
			}
		})
	}

	// An empty --case, as from an unset variable, must not mean "no case".
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"run", "--download", "none", "--case", "", "strings", file},
		{"run", "--download", "none", "strings", "--case", " ", file},
		{"run", "--dry-run", "--case", "", "strings", file},
	} {
		s := newCaseServer(t)
		_, _, err := runCase(t, s, args...)
		checkExit(t, err, exitUsage, "case reference must not be empty")
		if len(s.uploads) > 0 {
			t.Errorf("%q uploaded %v", args, s.uploads)
		}
	}
}

func TestCaseRefChecks(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		msg  string
	}{
		{[]string{"case", "create", ""}, "case reference must not be empty"},
		{[]string{"case", "set", "42", "a//b"}, `invalid case reference "a//b"`},
		{[]string{"folder", "list", "--case", "a/../b"}, `invalid case reference "a/../b"`},
		{[]string{"upload", file, "--case", ""}, "case reference must not be empty"},
	}
	for _, tt := range tests {
		s := newCaseServer(t)
		_, _, err := runCase(t, s, tt.args...)
		checkExit(t, err, exitUsage, tt.msg)
	}
}

func TestFolderTreeCase(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		exit     int
		contains string
	}{
		{"case folder", []string{"--case", "case-123"}, 0, "[43]  evidence/"},
		{"folder in the case", []string{"evidence", "--case", "case-123"}, 0, "[70]  disk.img"},
		{"folder outside the case", []string{"50", "--case", "case-123"}, exitUsage, "folder 50 is not in case case-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCaseServer(t)
			stdout, _, err := runCase(t, s, append([]string{"folder", "tree"}, tt.args...)...)
			checkExit(t, err, tt.exit, tt.contains)
			if tt.exit == 0 && !strings.Contains(stdout, tt.contains) {
				t.Errorf("stdout = %q, want %q", stdout, tt.contains)
			}
		})
	}
}

func TestWorkflowListCase(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		exit     int
		contains string
		excludes string
	}{
		{"whole case", []string{"--case", "case-123", "--recursive"}, 0, "workflow 5", "workflow 6"},
		{"folder in the case", []string{"evidence", "--case", "case-123"}, 0, "workflow 5", "workflow 6"},
		{"folder outside the case", []string{"50", "--case", "case-123"}, exitUsage, "folder 50 is not in case case-123", ""},
		{"neither FOLDER nor --case", nil, exitUsage, "give a FOLDER, or --case REF", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCaseServer(t)
			stdout, _, err := runCase(t, s, append([]string{"workflow", "list"}, tt.args...)...)
			checkExit(t, err, tt.exit, tt.contains)
			if tt.exit == 0 && (!strings.Contains(stdout, tt.contains) || strings.Contains(stdout, tt.excludes)) {
				t.Errorf("stdout = %q, want %q and not %q", stdout, tt.contains, tt.excludes)
			}
		})
	}
}
