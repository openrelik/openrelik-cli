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
	"testing"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
)

func TestDynamicWorkerCommands(t *testing.T) {
	// Create a temporary directory for config
	tmpDir, err := os.MkdirTemp("", "openrelik-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config.SetBaseDir(tmpDir)

	// Pre-populate workers cache
	testWorkers := []openrelik.Worker{
		{
			TaskName:    "openrelik-worker-strings.tasks.strings",
			DisplayName: "Strings",
			Description: "Extract strings from files",
			TaskConfig: []openrelik.TaskConfig{
				{
					Name:        "min-len",
					Description: "Minimum string length",
					Type:        "integer",
					Default:     4,
				},
			},
		},
		{
			TaskName:    "openrelik-worker-grep.tasks.grep",
			DisplayName: "Grep",
			Description: "Search for patterns in files",
			TaskConfig: []openrelik.TaskConfig{
				{
					Name:        "regex",
					Description: "Regex pattern",
					Type:        "string",
					Required:    true,
				},
			},
		},
	}

	if err := config.SaveWorkersCache(testWorkers); err != nil {
		t.Fatalf("Failed to save workers cache: %v", err)
	}

	// Create run command
	runCmd := newRunCmd()

	// Verify subcommands exist
	subCommands := runCmd.Commands()
	if len(subCommands) != 2 {
		t.Errorf("Expected 2 subcommands, got %d", len(subCommands))
	}

	foundStrings := false
	foundGrep := false
	for _, cmd := range subCommands {
		if cmd.Name() == "strings" {
			foundStrings = true
			// Check flags
			if cmd.Flags().Lookup("min-len") == nil {
				t.Errorf("strings command missing 'min-len' flag")
			}
		}
		if cmd.Name() == "grep" {
			foundGrep = true
			// Check flags
			if cmd.Flags().Lookup("regex") == nil {
				t.Errorf("grep command missing 'regex' flag")
			}
		}
	}

	if !foundStrings {
		t.Errorf("strings subcommand not found")
	}
	if !foundGrep {
		t.Errorf("grep subcommand not found")
	}
}

func TestRunCmdOutputDirValidation(t *testing.T) {
	tmpDir := t.TempDir()
	config.SetBaseDir(tmpDir)
	defer config.SetBaseDir("")

	testWorkers := []openrelik.Worker{
		{TaskName: "openrelik-worker-strings.tasks.strings", DisplayName: "Strings"},
	}
	if err := config.SaveWorkersCache(testWorkers); err != nil {
		t.Fatalf("Failed to save workers cache: %v", err)
	}

	os.Setenv("OPENRELIK_API_KEY", "test-key")
	os.Setenv("OPENRELIK_SERVER_URL", "http://localhost:19999")
	defer func() {
		os.Unsetenv("OPENRELIK_API_KEY")
		os.Unsetenv("OPENRELIK_SERVER_URL")
	}()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"run", "-o", "/nonexistent/output/dir", "strings", "123"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent output directory, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' in error, got: %v", err)
	}
}

func TestSliceArgs(t *testing.T) {
	tests := []struct {
		name       string
		workerName string
		args       []string
		want       [][]string
		wantDelim  string
		wantErr    bool
	}{
		{
			name:       "Single worker, single arg",
			workerName: "strings",
			args:       []string{"./users.go"},
			want:       [][]string{{"strings", "./users.go"}},
			wantDelim:  "",
			wantErr:    false,
		},
		{
			name:       "Chained workers",
			workerName: "strings",
			args:       []string{"./users.go", "--then", "grep", "--regex", "foo"},
			want: [][]string{
				{"strings", "./users.go"},
				{"grep", "--regex", "foo"},
			},
			wantDelim: "--then",
			wantErr:   false,
		},
		{
			name:       "Parallel workers",
			workerName: "strings",
			args:       []string{"./users.go", "--and", "grep", "--regex", "foo"},
			want: [][]string{
				{"strings", "./users.go"},
				{"grep", "--regex", "foo"},
			},
			wantDelim: "--and",
			wantErr:   false,
		},
		{
			name:       "Mixed delimiters (error)",
			workerName: "strings",
			args:       []string{"./users.go", "--then", "grep", "--and", "worker3"},
			want:       nil,
			wantDelim:  "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, delim, err := util.SliceArgs(tt.workerName, tt.args, "--then", "--and")
			if (err != nil) != tt.wantErr {
				t.Errorf("SliceArgs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if delim != tt.wantDelim {
					t.Errorf("SliceArgs() delim = %v, want %v", delim, tt.wantDelim)
				}
				if len(got) != len(tt.want) {
					t.Errorf("SliceArgs() len(got) = %v, want %v", len(got), len(tt.want))
					return
				}
				for i := range got {
					if len(got[i]) != len(tt.want[i]) {
						t.Errorf("SliceArgs() segment %d len = %v, want %v", i, len(got[i]), len(tt.want[i]))
						continue
					}
					for j := range got[i] {
						if got[i][j] != tt.want[i][j] {
							t.Errorf("SliceArgs() got[%d][%d] = %v, want %v", i, j, got[i][j], tt.want[i][j])
						}
					}
				}
			}
		})
	}
}

// TestRunFailedWorkflow checks that 'run' exits with an error when the
// workflow fails, after downloading the results that were produced.
func TestRunFailedWorkflow(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	testWorkers := []openrelik.Worker{
		{TaskName: "openrelik-worker-strings.tasks.strings", DisplayName: "Strings"},
	}
	if err := config.SaveWorkersCache(testWorkers); err != nil {
		t.Fatalf("Failed to save workers cache: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch path := r.URL.Path; {
		case strings.HasSuffix(path, "/workflows/") || strings.HasSuffix(path, "/run/"):
			fmt.Fprint(w, `{"id": 100, "folder": {"id": 1}}`)
		case strings.HasSuffix(path, "/status/"):
			fmt.Fprint(w, `{"status": "FAILURE", "tasks": [{"uuid": "t1", "display_name": "Strings", "status_short": "FAILURE"}]}`)
		case path == "/api/v1/workflows/100":
			fmt.Fprint(w, `{"id": 100, "tasks": [{"uuid": "t1", "id": 1, "display_name": "Strings",
				"output_files": [{"id": 10, "display_name": "partial.txt"}]}]}`)
		case path == "/api/v1/files/123":
			fmt.Fprint(w, `{"id": 123, "display_name": "input.bin", "folder": {"id": 1}}`)
		case path == "/api/v1/files/10":
			fmt.Fprint(w, `{"id": 10, "display_name": "partial.txt", "filesize": 7}`)
		case path == "/api/v1/files/10/download":
			fmt.Fprint(w, "partial")
		default:
			t.Logf("unhandled %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	outDir := t.TempDir()
	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs([]string{"run", "-o", outDir, "strings", "123"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "workflow 100 failed") {
		t.Fatalf("expected error containing %q, got %v", "workflow 100 failed", err)
	}
	if got, _ := os.ReadFile(filepath.Join(outDir, "partial.txt")); string(got) != "partial" {
		t.Errorf("expected the results to be downloaded, got %q", got)
	}

	// The workflow ID is shown when it starts, and the outcome and saved
	// files are printed as the result.
	if !strings.Contains(stderr.String(), "Workflow 100 started in folder 1\n") {
		t.Errorf("expected the workflow ID on stderr, got %q", stderr.String())
	}
	wantOut := fmt.Sprintf("Workflow 100 finished: FAILURE (folder 1)\nFile 10 saved to %q\n  SHA256 ", filepath.Join(outDir, "partial.txt"))
	if !strings.HasPrefix(stdout.String(), wantOut) {
		t.Errorf("stdout = %q, want it to start with %q", stdout.String(), wantOut)
	}
}

func TestRunDryRunAndSegmentFlags(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")

	testWorkers := []openrelik.Worker{
		{
			TaskName:    "openrelik-worker-strings.tasks.strings",
			DisplayName: "Strings",
			Description: "Extract strings from files",
		},
		{
			TaskName:    "openrelik-worker-grep.tasks.grep",
			DisplayName: "Grep",
			Description: "Search for patterns in files",
			TaskConfig: []openrelik.TaskConfig{
				{Name: "regex", Type: "string"},
			},
		},
	}
	if err := config.SaveWorkersCache(testWorkers); err != nil {
		t.Fatalf("Failed to save workers cache: %v", err)
	}

	tests := []struct {
		name           string
		args           []string
		wantQuiet      bool
		wantStderrText string
	}{
		{
			name:           "dry-run before worker",
			args:           []string{"run", "--dry-run", "strings", "123"},
			wantStderrText: "Dry run: generating workflow spec...",
		},
		{
			name:           "dry-run after worker before input",
			args:           []string{"run", "strings", "--dry-run", "123"},
			wantStderrText: "Dry run: generating workflow spec...",
		},
		{
			name:           "dry-run after input",
			args:           []string{"run", "strings", "123", "--dry-run"},
			wantStderrText: "Dry run: generating workflow spec...",
		},
		{
			name:           "chained workers with dry-run at end",
			args:           []string{"run", "strings", "--then", "grep", "--regex", "password", "100", "--dry-run"},
			wantStderrText: "Inputs: [100]",
		},
		{
			name:      "quiet flag after worker suppresses output",
			args:      []string{"run", "strings", "123", "--dry-run", "-q"},
			wantQuiet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputFormat = "human"
			quiet = false
			defer func() {
				outputFormat = "human"
				quiet = false
			}()

			root := NewRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(tt.args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}

			if tt.wantQuiet {
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Errorf("expected empty stdout/stderr with -q, got stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
				return
			}

			var spec util.WorkflowSpec
			if err := json.Unmarshal(stdout.Bytes(), &spec); err != nil {
				t.Fatalf("stdout is not valid JSON: %v\nstdout:\n%s", err, stdout.String())
			}
			if len(spec.Workflow.Tasks) == 0 {
				t.Errorf("expected at least one task in workflow spec, got 0")
			}
			if !strings.Contains(stderr.String(), tt.wantStderrText) {
				t.Errorf("expected stderr to contain %q, got %q", tt.wantStderrText, stderr.String())
			}
		})
	}
}

func TestRunWorkerHelp(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")

	testWorkers := []openrelik.Worker{
		{
			TaskName:    "openrelik-worker-strings.tasks.strings",
			DisplayName: "Strings",
			Description: "Extract strings from files",
			TaskConfig: []openrelik.TaskConfig{
				{Name: "min-len", Description: "Minimum string length", Type: "integer", Default: float64(4)},
			},
		},
		{
			TaskName:    "openrelik-worker-grep.tasks.grep",
			DisplayName: "Grep",
			Description: "Search for patterns in files",
			TaskConfig: []openrelik.TaskConfig{
				{Name: "regex", Description: "Regex pattern", Type: "string"},
			},
		},
	}
	if err := config.SaveWorkersCache(testWorkers); err != nil {
		t.Fatalf("Failed to save workers cache: %v", err)
	}

	tests := []struct {
		name         string
		args         []string
		wantContains []string
	}{
		{
			name: "single worker help",
			args: []string{"run", "strings", "--help"},
			wantContains: []string{
				"openrelik run strings <INPUT>... [flags]",
				"--min-len",
				"--then",
				"--and",
				"--output-dir",
				"--dry-run",
			},
		},
		{
			name: "chained worker help shows last segment with help",
			args: []string{"run", "strings", "--then", "grep", "--help"},
			wantContains: []string{
				"openrelik run grep <INPUT>... [flags]",
				"--regex",
				"--then",
				"--output-dir",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(tt.args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}

			out := stdout.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("expected help output to contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}
