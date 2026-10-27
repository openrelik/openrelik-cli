package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
)

// testRole is a role held by the mock sharing server.
type testRole struct {
	id     int
	folder int
	kind   string // "user" or "group"
	name   string
	role   string
}

// shareServer is a mock API for the sharing commands. It serves this tree,
// and keeps the roles on the folders in memory:
//
//	/Case A/            (42)  owned by "me"
//	/Case A/evidence/   (43)  notes.txt (9)
type shareServer struct {
	*httptest.Server
	mu         sync.Mutex
	nextID     int
	roles      []testRole
	requests   []string // "METHOD path" of the requests that change roles
	conflict   bool     // POST to roles answers 409
	forbidden  bool     // POST to roles answers 403
	failDelete bool     // DELETE of roles answers 500
}

var shareUsers = map[string]int{"me": 7, "alice": 17, "bob": 18}
var shareGroups = map[string]int{"ir": 2}

func newShareServer(t *testing.T) *shareServer {
	t.Helper()
	s := &shareServer{nextID: 100, roles: []testRole{{id: 1, folder: 42, kind: "user", name: "me", role: "Owner"}}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *shareServer) folderJSON(id int) string {
	names := map[int]string{42: "Case A", 43: "evidence"}
	var users, groups []string
	for _, r := range s.roles {
		if r.folder != id {
			continue
		}
		if r.kind == "user" {
			users = append(users, fmt.Sprintf(`{"id": %d, "role": %q, "user": {"id": %d, "username": %q, "display_name": %q}}`,
				r.id, r.role, shareUsers[r.name], r.name, strings.ToUpper(r.name[:1])+r.name[1:]))
		} else {
			groups = append(groups, fmt.Sprintf(`{"id": %d, "role": %q, "group": {"id": %d, "name": %q}}`,
				r.id, r.role, shareGroups[r.name], r.name))
		}
	}
	parent := ""
	if id == 43 {
		parent = `, "parent": ` + s.folderJSON(42)
	}
	return fmt.Sprintf(`{"id": %d, "display_name": %q, "user": {"username": "me"}, "user_roles": [%s], "group_roles": [%s]%s}`,
		id, names[id], strings.Join(users, ","), strings.Join(groups, ","), parent)
}

func (s *shareServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if r.Method != http.MethodGet {
		s.requests = append(s.requests, r.Method+" "+path)
	}

	switch {
	case path == "/users/me/":
		fmt.Fprint(w, `{"id": 7, "username": "me"}`)
	case path == "/folders/all/":
		fmt.Fprintf(w, `{"folders": [%s], "page": 1, "page_size": 100, "total_count": 1}`, s.folderJSON(42))
	case path == "/folders/shared/":
		fmt.Fprint(w, `[{"id": 50, "display_name": "From Bob", "user": {"username": "bob"}}]`)
	case path == "/folders/42/folders/":
		fmt.Fprint(w, `[{"id": 43, "display_name": "evidence"}]`)
	case path == "/folders/43/folders/":
		fmt.Fprint(w, `[]`)
	case path == "/folders/42/files/":
		fmt.Fprint(w, `[]`)
	case path == "/folders/43/files/":
		fmt.Fprint(w, `[{"id": 9, "display_name": "notes.txt"}]`)
	case path == "/files/9":
		fmt.Fprint(w, `{"id": 9, "display_name": "notes.txt", "folder": {"id": 43}}`)
	case len(parts) == 2 && parts[0] == "folders" && (parts[1] == "42" || parts[1] == "43"):
		id, _ := strconv.Atoi(parts[1])
		fmt.Fprint(w, s.folderJSON(id))
	case len(parts) == 4 && parts[2] == "roles" && parts[3] == "me":
		fmt.Fprint(w, `{"id": 1, "role": "Owner", "user_id": 7, "folder_id": 42}`)
	case len(parts) == 3 && parts[2] == "roles" && r.Method == http.MethodPost:
		s.share(w, r, parts[1])
	case len(parts) == 5 && parts[2] == "roles" && r.Method == http.MethodDelete:
		if s.failDelete {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"detail": "boom"}`)
			return
		}
		roleID, _ := strconv.Atoi(parts[4])
		s.roles = slices.DeleteFunc(s.roles, func(r testRole) bool { return r.id == roleID })
		fmt.Fprint(w, `null`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"detail": "Not found."}`)
	}
}

func (s *shareServer) share(w http.ResponseWriter, r *http.Request, folder string) {
	if s.forbidden {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail": "Insufficient permissions."}`)
		return
	}
	if s.conflict {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"detail": "already has the role"}`)
		return
	}
	var req struct {
		UserNames  []string `json:"user_names"`
		GroupNames []string `json:"group_names"`
		UserRole   string   `json:"user_role"`
		GroupRole  string   `json:"group_role"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	id, _ := strconv.Atoi(folder)
	add := func(kind string, names []string, role string, known map[string]int) bool {
		for _, n := range names {
			if _, ok := known[n]; !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprintf(w, `{"detail": "%s %q not found."}`, kind, n)
				return false
			}
			s.nextID++
			s.roles = append(s.roles, testRole{id: s.nextID, folder: id, kind: kind, name: n, role: role})
		}
		return true
	}
	if add("user", req.UserNames, req.UserRole, shareUsers) && add("group", req.GroupNames, req.GroupRole, shareGroups) {
		fmt.Fprint(w, s.folderJSON(id))
	}
}

// rolesOn returns "kind name Role" for the roles on folder.
func (s *shareServer) rolesOn(folder int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.roles {
		if r.folder == folder {
			out = append(out, r.kind+" "+r.name+" "+r.role)
		}
	}
	return out
}

func (s *shareServer) addRole(folder int, kind, name, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.roles = append(s.roles, testRole{id: s.nextID, folder: folder, kind: kind, name: name, role: role})
}

// runShare executes the CLI with args against s, with stdin as input, and
// returns stdout, stderr and the error from Execute.
func runShare(t *testing.T, s *shareServer, stdin string, args ...string) (string, string, error) {
	t.Helper()
	config.SetBaseDir(t.TempDir())
	t.Cleanup(func() { config.SetBaseDir("") })
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", s.URL)

	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// fakeStdinTerminal makes stdin count as a terminal for the rest of the test.
func fakeStdinTerminal(t *testing.T) {
	t.Helper()
	orig := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = orig })
}

type shareOutput struct {
	Folder struct {
		ID   int    `json:"id"`
		Path string `json:"path"`
	} `json:"folder"`
	Results []struct {
		Type          string   `json:"type"`
		Name          string   `json:"name"`
		Role          string   `json:"role"`
		PreviousRoles []string `json:"previous_roles"`
		Status        string   `json:"status"`
		Error         string   `json:"error"`
	} `json:"results"`
}

func parseShareOutput(t *testing.T, stdout string) shareOutput {
	t.Helper()
	var out shareOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	return out
}

func TestFolderShare(t *testing.T) {
	s := newShareServer(t)

	stdout, _, err := runShare(t, s, "", "folder", "share", "Case A/", "--user", "alice", "--group", "ir", "--role", "Viewer")
	if err != nil {
		t.Fatalf("share failed: %v", err)
	}
	for _, want := range []string{`Folder 42 "Case A" shared:`, "user   alice  Viewer", "group  ir     Viewer"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	want := []string{"user me Owner", "user alice Viewer", "group ir Viewer"}
	if got := s.rolesOn(42); !slices.Equal(got, want) {
		t.Errorf("roles = %v, want %v", got, want)
	}

	// Sharing again with the same role changes nothing.
	s.requests = nil
	stdout, _, err = runShare(t, s, "", "folder", "share", "42", "--user", "alice", "--role", "viewer", "--format", "json")
	if err != nil {
		t.Fatalf("share failed: %v", err)
	}
	out := parseShareOutput(t, stdout)
	if out.Folder.ID != 42 || out.Folder.Path != "/Case A/" || len(out.Results) != 1 || out.Results[0].Status != "unchanged" {
		t.Errorf("unexpected output: %+v", out)
	}
	if len(s.requests) != 0 {
		t.Errorf("unchanged share sent requests: %v", s.requests)
	}

	// A new role replaces the old one: added first, then the old one removed.
	stdout, _, err = runShare(t, s, "", "folder", "share", "42", "--user", "alice", "--role", "editor", "--format", "json")
	if err != nil {
		t.Fatalf("share failed: %v", err)
	}
	out = parseShareOutput(t, stdout)
	r := out.Results[0]
	if r.Status != "changed" || r.Role != "editor" || !slices.Equal(r.PreviousRoles, []string{"viewer"}) {
		t.Errorf("unexpected result: %+v", r)
	}
	if !strings.HasPrefix(s.requests[0], "POST ") || !strings.HasPrefix(s.requests[1], "DELETE /folders/42/roles/users/") {
		t.Errorf("requests = %v, want the POST before the DELETE", s.requests)
	}
	want = []string{"user me Owner", "group ir Viewer", "user alice Editor"}
	if got := s.rolesOn(42); !slices.Equal(got, want) {
		t.Errorf("roles = %v, want %v", got, want)
	}
}

func TestFolderShareConflictIsUnchanged(t *testing.T) {
	s := newShareServer(t)
	s.conflict = true
	stdout, _, err := runShare(t, s, "", "folder", "share", "42", "--user", "alice", "--role", "viewer", "--format", "json")
	if err != nil {
		t.Fatalf("share failed: %v", err)
	}
	if out := parseShareOutput(t, stdout); out.Results[0].Status != "unchanged" {
		t.Errorf("status = %q, want unchanged", out.Results[0].Status)
	}
}

func TestFolderSharePartialFailure(t *testing.T) {
	s := newShareServer(t)
	stdout, _, err := runShare(t, s, "", "folder", "share", "42", "--user", "nobody", "--user", "alice", "--role", "viewer", "--format", "json")
	if err == nil {
		t.Fatal("share with an unknown user succeeded")
	}
	if code := exitCode(err); code != exitNotFound {
		t.Errorf("exit code = %d, want %d", code, exitNotFound)
	}
	for _, want := range []string{"not shared with 1 of 2", `user nobody: no user named "nobody"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't contain %q", err, want)
		}
	}
	out := parseShareOutput(t, stdout)
	if out.Results[0].Status != "failed" || out.Results[1].Status != "added" {
		t.Errorf("unexpected results: %+v", out.Results)
	}
	if got := s.rolesOn(42); !slices.Contains(got, "user alice Viewer") {
		t.Errorf("alice wasn't shared with after the failure: %v", got)
	}
}

func TestFolderShareForbidden(t *testing.T) {
	s := newShareServer(t)
	s.forbidden = true
	_, _, err := runShare(t, s, "", "folder", "share", "42", "--user", "alice", "--role", "viewer")
	if err == nil {
		t.Fatal("share succeeded without permission")
	}
	if code := exitCode(err); code != exitAuth {
		t.Errorf("exit code = %d, want %d", code, exitAuth)
	}
	if !strings.Contains(err.Error(), "check yours with 'openrelik folder access 42'") {
		t.Errorf("error doesn't explain the roles: %v", err)
	}
}

func TestFolderShareDeleteFailure(t *testing.T) {
	s := newShareServer(t)
	s.addRole(42, "user", "alice", "Viewer")
	s.failDelete = true
	_, _, err := runShare(t, s, "", "folder", "share", "42", "--user", "alice", "--role", "editor")
	if err == nil {
		t.Fatal("share succeeded although the old role wasn't removed")
	}
	if !strings.Contains(err.Error(), "alice now has the roles Editor, Viewer") {
		t.Errorf("error doesn't say alice has both roles: %v", err)
	}
	if code := exitCode(err); code != exitUnavailable {
		t.Errorf("exit code = %d, want %d", code, exitUnavailable)
	}
}

func TestFolderShareInvalidArgs(t *testing.T) {
	s := newShareServer(t)
	for _, args := range [][]string{
		{"folder", "share", "42", "--user", "alice", "--role", "admin"},
		{"folder", "share", "42", "--user", "alice"},
		{"folder", "share", "42", "--role", "viewer"},
		{"folder", "unshare", "42"},
		{"folder", "list", "42", "--shared"},
	} {
		_, _, err := runShare(t, s, "", args...)
		if err == nil {
			t.Errorf("%v succeeded", args)
			continue
		}
		if code := exitCode(err); code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d (%v)", args, code, exitUsage, err)
		}
	}
	if len(s.requests) != 0 {
		t.Errorf("invalid commands sent requests: %v", s.requests)
	}
}

func TestFolderShareLastOwner(t *testing.T) {
	s := newShareServer(t)
	for _, args := range [][]string{
		{"folder", "unshare", "42", "--user", "me", "--force"},
		{"folder", "share", "42", "--user", "me", "--role", "editor", "--force"},
	} {
		_, _, err := runShare(t, s, "", args...)
		if err == nil || !strings.Contains(err.Error(), "would have no owner") {
			t.Errorf("%v: err = %v, want the last owner refused", args, err)
		}
	}
	if len(s.requests) != 0 {
		t.Errorf("refused changes sent requests: %v", s.requests)
	}

	// An owner on a parent folder keeps the subfolder owned.
	s.addRole(43, "user", "me", "Owner")
	if _, _, err := runShare(t, s, "", "folder", "unshare", "43", "--user", "me", "--force"); err != nil {
		t.Errorf("unshare with an owner on the parent failed: %v", err)
	}
}

func TestFolderShareOwnRole(t *testing.T) {
	s := newShareServer(t)
	s.addRole(42, "user", "alice", "Owner")

	// No terminal: fail and name the flag.
	_, _, err := runShare(t, s, "", "folder", "share", "42", "--user", "me", "--role", "viewer")
	if err == nil || !strings.Contains(err.Error(), "use --force") || !strings.Contains(err.Error(), "from Owner to Viewer") {
		t.Errorf("err = %v, want a refusal naming --force", err)
	}

	fakeStdinTerminal(t)
	_, stderr, err := runShare(t, s, "n\n", "folder", "unshare", "42", "--user", "me")
	if err == nil || err.Error() != "cancelled; access was not changed" {
		t.Errorf("err = %v, want cancelled", err)
	}
	if !strings.Contains(stderr, "removes your own Owner role") {
		t.Errorf("no question on stderr: %q", stderr)
	}
	if len(s.requests) != 0 {
		t.Errorf("cancelled change sent requests: %v", s.requests)
	}

	if _, _, err := runShare(t, s, "y\n", "folder", "unshare", "42", "--user", "me"); err != nil {
		t.Fatalf("confirmed unshare failed: %v", err)
	}
	if got := s.rolesOn(42); !slices.Equal(got, []string{"user alice Owner"}) {
		t.Errorf("roles = %v", got)
	}
}

func TestFolderUnshare(t *testing.T) {
	s := newShareServer(t)
	s.addRole(42, "user", "alice", "Viewer")
	s.addRole(43, "user", "alice", "Editor")
	s.addRole(43, "group", "ir", "Viewer")

	stdout, stderr, err := runShare(t, s, "", "folder", "unshare", "Case A/evidence", "--user", "alice", "--group", "ir", "--user", "bob")
	if err != nil {
		t.Fatalf("unshare failed: %v", err)
	}
	for _, want := range []string{`Folder 43 "evidence" unshared:`, "user   alice  removed Editor", "group  ir     removed Viewer"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{"user bob had no role", "user alice still has the Viewer role through /Case A/ (folder 42)"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if got := s.rolesOn(43); len(got) != 0 {
		t.Errorf("roles left on folder 43: %v", got)
	}
	if got := s.rolesOn(42); !slices.Contains(got, "user alice Viewer") {
		t.Errorf("the role on the parent was removed: %v", got)
	}
}

func TestFolderAccess(t *testing.T) {
	s := newShareServer(t)
	s.addRole(42, "user", "alice", "Viewer")
	s.addRole(43, "group", "ir", "Editor")

	stdout, _, err := runShare(t, s, "", "folder", "access", "43")
	if err != nil {
		t.Fatalf("access failed: %v", err)
	}
	for _, want := range []string{
		`Folder 43 "evidence" (/Case A/evidence/)`,
		"Your role: Owner (from /Case A/)",
		"user   me     Owner   /Case A/",
		"group  ir     Editor  this folder",
		"user   alice  Viewer  /Case A/",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}

	stdout, _, err = runShare(t, s, "", "folder", "access", "43", "--format", "json")
	if err != nil {
		t.Fatalf("access failed: %v", err)
	}
	var out struct {
		MyRole string `json:"my_role"`
		Users  []struct {
			Name          string `json:"name"`
			Role          string `json:"role"`
			InheritedFrom *struct {
				ID int `json:"id"`
			} `json:"inherited_from"`
		} `json:"users"`
		Groups []struct {
			Name          string `json:"name"`
			Role          string `json:"role"`
			InheritedFrom any    `json:"inherited_from"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if out.MyRole != "owner" || len(out.Users) != 2 || out.Users[1].Name != "alice" || out.Users[1].Role != "viewer" ||
		out.Users[1].InheritedFrom == nil || out.Users[1].InheritedFrom.ID != 42 {
		t.Errorf("unexpected users: %s", stdout)
	}
	if len(out.Groups) != 1 || out.Groups[0].Role != "editor" || out.Groups[0].InheritedFrom != nil {
		t.Errorf("unexpected groups: %s", stdout)
	}
}

func TestFileShareHint(t *testing.T) {
	s := newShareServer(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"folder", "share", "Case A/evidence/notes.txt", "--user", "alice", "--role", "viewer"},
			"openrelik folder share 'Case A/evidence/' --user alice --role viewer"},
		{[]string{"file", "share", "9", "--group", "ir team", "--role", "editor"},
			"openrelik folder share 43 --group 'ir team' --role editor"},
		{[]string{"file", "unshare", "Case A/evidence/notes.txt", "--user", "alice"},
			"openrelik folder unshare 43 --user alice"},
		{[]string{"folder", "access", "Case A/evidence/notes.txt"},
			"openrelik folder access 'Case A/evidence/'"},
	} {
		_, _, err := runShare(t, s, "", tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want it to contain %q", tc.args, err, tc.want)
			continue
		}
		if code := exitCode(err); code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d", tc.args, code, exitUsage)
		}
	}
}

func TestFolderListShared(t *testing.T) {
	s := newShareServer(t)
	stdout, _, err := runShare(t, s, "", "folder", "list", "--shared")
	if err != nil {
		t.Fatalf("list --shared failed: %v", err)
	}
	if !strings.Contains(stdout, "OWNER") || !strings.Contains(stdout, "50  From Bob/     bob") {
		t.Errorf("unexpected output:\n%s", stdout)
	}

	stdout, _, err = runShare(t, s, "", "folder", "list", "--shared", "--format", "json")
	if err != nil {
		t.Fatalf("list --shared failed: %v", err)
	}
	var out folderContents
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || len(out.Folders) != 1 || out.Folders[0].ID != 50 || out.Files == nil {
		t.Errorf("unexpected JSON (%v):\n%s", err, stdout)
	}
}
