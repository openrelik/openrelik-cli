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
	"os"
	"strings"
	"testing"

	"github.com/openrelik/openrelik-go-client"
	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
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
