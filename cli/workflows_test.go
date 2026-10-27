package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openrelik/openrelik-cli/util"
)

func TestWorkflowCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Info
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/123" {
			fmt.Fprintln(w, `{"id": 123, "display_name": "Test Workflow", "folder": {"id": 1}}`)
			return
		}

		// Status
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/folders/1/workflows/123/status/" {
			fmt.Fprintln(w, `{"status": "completed", "tasks": []}`)
			return
		}

		// Run
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/1/workflows/123/run/" {
			fmt.Fprintln(w, `{"id": 123, "display_name": "Running Workflow"}`)
			return
		}

		// Create (resolving folder ID from file 456)
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/files/456" {
			fmt.Fprintln(w, `{"id": 456, "folder": {"id": 1}}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/1/workflows/" {
			fmt.Fprintln(w, `{"id": 124, "display_name": "New Workflow", "folder": {"id": 1}}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/1/workflows/124/run/" {
			fmt.Fprintln(w, `{"id": 124, "display_name": "New Workflow Running"}`)
			return
		}

		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error": "not found: %s"}`, r.URL.Path)
	}))
	defer server.Close()

	os.Setenv("OPENRELIK_API_KEY", "test-key")
	os.Setenv("OPENRELIK_SERVER_URL", server.URL)
	defer func() {
		os.Unsetenv("OPENRELIK_API_KEY")
		os.Unsetenv("OPENRELIK_SERVER_URL")
	}()

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "info",
			args:     []string{"workflow", "info", "123"},
			expected: "Test Workflow",
		},
		{
			name:     "status",
			args:     []string{"workflow", "status", "123"},
			expected: "Status: completed",
		},
		{
			name:     "run",
			args:     []string{"workflow", "run", "123"},
			expected: "Running Workflow",
		},
		{
			name:     "create",
			args:     []string{"workflow", "create", "--file", "456", "--template", "5"},
			expected: `"New Workflow" created (ID 124)`,
		},
		{
			name:     "create-and-run",
			args:     []string{"workflow", "create", "--file", "456", "--template", "5", "--run"},
			expected: "New Workflow Running",
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
			if !strings.Contains(output, tt.expected) {
				t.Errorf("expected output to contain %q, but it was %q", tt.expected, output)
			}
		})
	}
}

func TestWorkflowRunSpec(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Get workflow (called first by run cmd)
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/123" {
			fmt.Fprintln(w, `{"id": 123, "folder": {"id": 1}, "spec_json": "{\"test\":\"spec\"}"}`)
			return
		}

		// Run workflow
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/1/workflows/123/run/" {
			body, _ := io.ReadAll(r.Body)
			var req struct {
				WorkflowSpec json.RawMessage `json:"workflow_spec"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			// Verify that the spec_json from the workflow was passed
			if string(req.WorkflowSpec) != `{"test":"spec"}` {
				w.WriteHeader(http.StatusUnprocessableEntity)
				fmt.Fprintf(w, `{"error": "expected spec {\"test\":\"spec\"}, got %s"}`, string(req.WorkflowSpec))
				return
			}

			fmt.Fprintln(w, `{"id": 123, "display_name": "Running Workflow"}`)
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

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"workflow", "run", "123"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Running Workflow") {
		t.Errorf("expected output to contain 'Running Workflow', but it was %q", output)
	}
}

// newWorkflowServer is a mock API with a case folder 10 holding workflow
// 201, its subfolder 20 holding workflow 200, and folder 31 two levels down
// holding workflow 202. Workflow 200 chains "Extract" into "Grep", whose
// output files 300 and 301 contain "hello".
func newWorkflowServer(t *testing.T) *httptest.Server {
	t.Helper()
	spec := `{"workflow": {"type": "chain", "tasks": [
		{"uuid": "u-extract", "display_name": "Extract", "task_config": [], "tasks": [
			{"uuid": "u-grep", "display_name": "Grep", "task_config": [
				{"name": "regex", "value": "pass word"}, {"name": "ignore-case", "value": true}]}]}]}}`
	specJSON, _ := json.Marshal(spec)
	routes := map[string]string{
		"/api/v1/workflows/200": `{"id": 200, "display_name": "Triage", "uuid": "wf-uuid",
			"created_at": "2026-10-02T10:00:00Z", "updated_at": "2026-10-02T10:05:00Z",
			"user": {"username": "analyst"}, "folder": {"id": 20},
			"template": {"id": 5, "display_name": "Triage template"},
			"files": [{"id": 100, "display_name": "disk.E01", "data_type": "file:generic"}],
			"spec_json": ` + string(specJSON) + `,
			"tasks": [{"id": 1, "uuid": "u-extract", "display_name": "Extract", "status_short": "SUCCESS", "runtime": 1.5,
				"created_at": "2026-10-02T10:00:00Z",
				"output_files": [{"id": 299, "display_name": "extracted.txt"}],
				"tasks": [{"id": 2, "uuid": "u-grep", "display_name": "Grep", "status_short": "SUCCESS", "runtime": 0.25,
					"output_files": [{"id": 300, "display_name": "matches.txt"}, {"id": 301, "display_name": "../../escape.txt"}]}]}]}`,
		"/api/v1/workflows/201": `{"id": 201, "display_name": "Older", "created_at": "2026-10-01T10:00:00Z", "folder": {"id": 10}, "tasks": []}`,
		"/api/v1/workflows/202": `{"id": 202, "display_name": "Deep", "created_at": "2026-10-03T10:00:00Z", "folder": {"id": 31},
			"tasks": [{"id": 3, "status_short": "FAILURE"}, {"id": 4, "status_short": "SUCCESS"}, {"id": 5, "status_short": "FAILURE"}]}`,
		"/api/v1/folders/20/workflows/200/status/": `{"status": "COMPLETE", "tasks": [
			{"id": 1, "uuid": "u-extract", "display_name": "Extract", "status_short": "SUCCESS"},
			{"id": 2, "uuid": "u-grep", "display_name": "Grep", "status_short": "SUCCESS"}]}`,
		"/api/v1/folders/10":          `{"id": 10, "workflows": [{"id": 201}]}`,
		"/api/v1/folders/30":          `{"id": 30, "workflows": []}`,
		"/api/v1/folders/31":          `{"id": 31, "workflows": [{"id": 202}]}`,
		"/api/v1/folders/40":          `{"id": 40, "workflows": []}`,
		"/api/v1/folders/40/folders/": `[]`,
		"/api/v1/folders/10/folders/": `[{"id": 20, "workflows": [{"id": 200}]}, {"id": 30, "workflows": []}]`,
		"/api/v1/folders/20/folders/": `[]`,
		"/api/v1/folders/30/folders/": `[{"id": 31, "workflows": [{"id": 202}]}]`,
		"/api/v1/folders/31/folders/": `[]`,
	}
	for _, id := range []string{"299", "300", "301"} {
		routes["/api/v1/files/"+id] = `{"id": ` + id + `, "display_name": "x", "filesize": 5,
			"hash_sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			fmt.Fprint(w, "hello")
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)
	return server
}

func runWorkflowCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	out := new(bytes.Buffer)
	root.SetOut(out)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestWorkflowInfoAndStatus(t *testing.T) {
	newWorkflowServer(t)

	tests := []struct {
		name string
		args []string
		want string // exact output, or substrings separated by "|"
	}{
		{
			name: "info",
			args: []string{"workflow", "info", "200"},
			want: `ID        200
Name      Triage
Folder    20
Template  Triage template (ID 5)
|
INPUT ID  INPUT FILE
100       disk.E01

TASK     STATUS   RUNTIME  OUTPUT ID  OUTPUT FILE
Extract  SUCCESS  1.5s     299        extracted.txt
Grep     SUCCESS  0.2s     300        matches.txt
                           301        ../../escape.txt

TASK  SETTINGS
Grep  regex="pass word" ignore-case=true
`,
		},
		{
			name: "info verbose",
			args: []string{"workflow", "info", "200", "--format", "verbose"},
			want: "UUID      wf-uuid|Owner     analyst|Created   2026-10-02T10:00:00Z|1   u-extract  Extract  SUCCESS  1.5s     2026-10-02T10:00:00Z",
		},
		{
			name: "status",
			args: []string{"workflow", "status", "200"},
			want: `Status: COMPLETE

TASK     STATUS   RUNTIME  OUTPUT ID  OUTPUT FILE
Extract  SUCCESS  1.5s     299        extracted.txt
Grep     SUCCESS  0.2s     300        matches.txt
                           301        ../../escape.txt
`,
		},
		{
			name: "status verbose keeps the task table",
			args: []string{"workflow", "status", "200", "--format", "verbose"},
			want: "Status: COMPLETE|2   u-grep     Grep     SUCCESS|301        ../../escape.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runWorkflowCmd(t, tt.args...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if !strings.Contains(tt.want, "|") {
				if out != tt.want {
					t.Errorf("output:\n%s\nwant:\n%s", out, tt.want)
				}
				return
			}
			for _, part := range strings.Split(tt.want, "|") {
				if !strings.Contains(out, part) {
					t.Errorf("expected output to contain %q\n%s", part, out)
				}
			}
		})
	}
}

func TestWorkflowList(t *testing.T) {
	newWorkflowServer(t)

	t.Run("human", func(t *testing.T) {
		out, err := runWorkflowCmd(t, "workflow", "list", "10")
		if err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID   NAME") ||
			!strings.HasPrefix(lines[1], "201  Older   10      not run") ||
			!strings.HasPrefix(lines[2], "200  Triage  20      2 SUCCESS") {
			t.Errorf("unexpected output:\n%s", out)
		}
	})

	tests := []struct {
		name string
		args []string
		want []int
	}{
		{name: "folder and its subfolders", args: []string{"10"}, want: []int{201, 200}},
		{name: "recursive", args: []string{"10", "--recursive"}, want: []int{201, 200, 202}},
		{name: "workflow folder", args: []string{"31"}, want: []int{202}},
		{name: "folder without workflows", args: []string{"40"}, want: []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runWorkflowCmd(t, append([]string{"workflow", "list", "--format", "json"}, tt.args...)...)
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			var got []struct{ ID int }
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, out)
			}
			ids := []int{}
			for _, w := range got {
				ids = append(ids, w.ID)
			}
			if fmt.Sprint(ids) != fmt.Sprint(tt.want) {
				t.Errorf("listed workflows %v, want %v", ids, tt.want)
			}
		})
	}

	t.Run("task summary", func(t *testing.T) {
		out, err := runWorkflowCmd(t, "workflow", "list", "30")
		if err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}
		if !strings.Contains(out, "2 FAILURE, 1 SUCCESS") {
			t.Errorf("expected task counts in output:\n%s", out)
		}
	})
}

func TestWorkflowDownload(t *testing.T) {
	newWorkflowServer(t)
	const helloSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	tests := []struct {
		name      string
		args      []string
		wantFiles []string
		wantErr   string
	}{
		{name: "last task only", wantFiles: []string{"matches.txt", "escape.txt"}},
		{name: "all tasks", args: []string{"--all"}, wantFiles: []string{"extracted.txt", "matches.txt", "escape.txt"}},
		{name: "task folders", args: []string{"--all", "--task-folders"}, wantFiles: []string{"Extract_1/extracted.txt", "Grep_2/matches.txt", "Grep_2/escape.txt"}},
		{name: "invalid ID", args: []string{"--all"}, wantErr: `invalid workflow ID: "x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "out")
			os.Mkdir(dir, 0o755)
			id := "200"
			if tt.wantErr != "" {
				id = "x"
			}
			out, err := runWorkflowCmd(t, append([]string{"workflow", "download", id, dir}, tt.args...)...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			var n int
			filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				if !d.IsDir() {
					n++
				}
				return nil
			})
			if n != len(tt.wantFiles) {
				t.Errorf("saved %d files, want %d", n, len(tt.wantFiles))
			}
			for _, f := range tt.wantFiles {
				if got, _ := os.ReadFile(filepath.Join(dir, f)); string(got) != "hello" {
					t.Errorf("expected %s to hold the download, got %q", f, got)
				}
				if !strings.Contains(out, fmt.Sprintf("saved to %q\n  SHA256 %s (verified)", filepath.Join(dir, f), helloSHA256)) {
					t.Errorf("expected output to report %s\n%s", f, out)
				}
			}
		})
	}

	t.Run("missing directory", func(t *testing.T) {
		_, err := runWorkflowCmd(t, "workflow", "download", "200", filepath.Join(t.TempDir(), "nope"))
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected a missing directory error, got %v", err)
		}
	})
}

// newWaitServer serves workflow 99 in folder 9, whose status requests get
// the statuses in turn, repeating the last one. A workflow without tasks is
// served as 98. It returns a pointer to the number of status requests.
func newWaitServer(t *testing.T, statuses ...string) *int {
	t.Helper()
	var mu sync.Mutex
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/workflows/99":
			fmt.Fprint(w, `{"id": 99, "folder": {"id": 9}, "tasks": [
				{"id": 1, "uuid": "u-strings", "display_name": "Strings", "runtime": 2,
					"output_files": [{"id": 500, "display_name": "strings.txt"}]}]}`)
		case "/api/v1/workflows/98":
			fmt.Fprint(w, `{"id": 98, "folder": {"id": 9}, "tasks": []}`)
		case "/api/v1/folders/9/workflows/99/status/":
			mu.Lock()
			s := statuses[min(polls, len(statuses)-1)]
			polls++
			mu.Unlock()
			fmt.Fprintf(w, `{"status": %q, "tasks": [
				{"id": 1, "uuid": "u-strings", "display_name": "Strings", "status_short": %q}]}`, s, s)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	interval := util.StatusPollInterval
	util.StatusPollInterval = time.Millisecond
	t.Cleanup(func() { util.StatusPollInterval = interval })
	return &polls
}

func TestWorkflowWait(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []string
		args      []string
		want      string // substrings of the output, separated by "|"
		wantErr   string
		wantExit  int
		wantPolls int // 0 skips the check
	}{
		{
			name:      "success",
			statuses:  []string{"PENDING", "STARTED", "SUCCESS"},
			args:      []string{"workflow", "wait", "99"},
			want:      "Status: SUCCESS|Strings  SUCCESS  2.0s     500        strings.txt",
			wantPolls: 4, // three while waiting, one for the result
		},
		{
			name:     "failure prints the status",
			statuses: []string{"STARTED", "FAILURE"},
			args:     []string{"workflow", "wait", "99"},
			want:     "Status: FAILURE|Strings  FAILURE",
			wantErr:  "workflow 99 failed",
			wantExit: exitError,
		},
		{
			name:      "already finished",
			statuses:  []string{"SUCCESS"},
			args:      []string{"workflow", "wait", "99"},
			want:      "Status: SUCCESS",
			wantPolls: 2,
		},
		{
			name:     "json",
			statuses: []string{"SUCCESS"},
			args:     []string{"workflow", "wait", "99", "--format", "json"},
			want:     `"status": "SUCCESS"|"display_name": "strings.txt"`,
		},
		{
			name:     "timeout",
			statuses: []string{"STARTED"},
			args:     []string{"workflow", "wait", "99", "--timeout", "20ms"},
			wantErr:  "workflow 99 is still running after 20ms; it keeps running on the server",
			wantExit: exitError,
		},
		{
			name:     "never started",
			statuses: []string{"SUCCESS"},
			args:     []string{"workflow", "wait", "98"},
			wantErr:  "workflow 98 hasn't been started; start it with 'openrelik workflow run 98'",
			wantExit: exitError,
		},
		{
			name:     "not found",
			statuses: []string{"SUCCESS"},
			args:     []string{"workflow", "wait", "97"},
			wantErr:  "workflow 97 not found",
			wantExit: exitNotFound,
		},
		{
			name:     "bad ID",
			statuses: []string{"SUCCESS"},
			args:     []string{"workflow", "wait", "abc"},
			wantErr:  `invalid workflow ID: "abc"`,
			wantExit: exitUsage,
		},
		{
			name:     "negative timeout",
			statuses: []string{"SUCCESS"},
			args:     []string{"workflow", "wait", "99", "--timeout", "-1s"},
			wantErr:  "invalid timeout: -1s (must be 0 or greater)",
			wantExit: exitUsage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			polls := newWaitServer(t, tt.statuses...)
			out, err := runWorkflowCmd(t, tt.args...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Execute() failed: %v", err)
				}
			} else {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				if got := exitCode(err); got != tt.wantExit {
					t.Errorf("exitCode = %d, want %d", got, tt.wantExit)
				}
			}
			for _, part := range strings.Split(tt.want, "|") {
				if !strings.Contains(out, part) {
					t.Errorf("expected output to contain %q\n%s", part, out)
				}
			}
			if tt.want == "" && out != "" {
				t.Errorf("expected no output, got:\n%s", out)
			}
			if tt.wantPolls != 0 && *polls != tt.wantPolls {
				t.Errorf("status requests = %d, want %d", *polls, tt.wantPolls)
			}
		})
	}
}
