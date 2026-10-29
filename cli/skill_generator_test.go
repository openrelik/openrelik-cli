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
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-go-client"
)

var updateGolden = flag.Bool("update", false, "update the golden files in testdata")

func TestGetTemplateContext(t *testing.T) {
	workers := []openrelik.Worker{
		{
			TaskName:    "test_worker",
			DisplayName: "Test Worker",
		},
		{
			TaskName:    "openrelik-worker-strings.tasks.strings",
			DisplayName: "Strings",
			Description: "Extract strings\n  from files.",
		},
		{
			TaskName:    "",
			DisplayName: "Empty",
			Description: "Should be skipped.",
		},
		{
			// 'run strings' runs the first worker named strings.
			TaskName:    "other-worker.tasks.strings",
			Description: "Another worker named strings.",
		},
	}

	ctx := getTemplateContext(workers)

	want := []SkillWorker{
		{CommandName: "strings", Description: "Extract strings from files."},
		{CommandName: "test_worker", Description: "Test Worker"},
	}
	if !reflect.DeepEqual(ctx.Workers, want) {
		t.Errorf("Workers = %+v, want %+v", ctx.Workers, want)
	}
	if !strings.HasSuffix(ctx.Description, " Workers: strings, test_worker.") {
		t.Errorf("Description doesn't name the workers: %q", ctx.Description)
	}
}

func TestSkillDescription(t *testing.T) {
	if got := skillDescription(nil); got != skillDescriptionBase {
		t.Errorf("no workers: got %q", got)
	}

	var many []SkillWorker
	for i := range 200 {
		many = append(many, SkillWorker{CommandName: fmt.Sprintf("worker_%03d", i)})
	}
	got := skillDescription(many)
	if len(got) > maxSkillDescription {
		t.Errorf("description is %d bytes, more than %d", len(got), maxSkillDescription)
	}
	m := regexp.MustCompile(`worker_(\d+), and (\d+) more\.$`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("expected the description to end with the number left out, got %q", got)
	}
	// Every worker is either named or counted.
	last, _ := strconv.Atoi(m[1])
	more, _ := strconv.Atoi(m[2])
	if last+1+more != len(many) {
		t.Errorf("named %d and counted %d more, want %d in all", last+1, more, len(many))
	}

	// A name too long to fit is counted, not dropped.
	long := []SkillWorker{{CommandName: strings.Repeat("x", maxSkillDescription)}, {CommandName: "strings"}}
	if got, want := skillDescription(long), skillDescriptionBase+" Workers: 2, listed in the skill."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReadOnlyCommands(t *testing.T) {
	root := NewRootCmd()
	for _, c := range readOnlyCommands {
		found, _, err := root.Find(strings.Fields(c))
		if err != nil || found.CommandPath() != "openrelik "+c {
			t.Errorf("%q is not a command", c)
		}
	}
}

// frontmatter returns the keys and values of the YAML frontmatter of a
// rendered SKILL.md.
func frontmatter(t *testing.T, content string) map[string]string {
	t.Helper()
	parts := strings.SplitN(content, "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatalf("no frontmatter in:\n%s", content)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("bad frontmatter line %q", line)
		}
		fields[key] = value
	}
	return fields
}

func TestRenderSkill(t *testing.T) {
	workers := []SkillWorker{
		{CommandName: "grep", Description: "Search files for a pattern"},
		{CommandName: "strings", Description: "Extract strings from files"},
	}
	data := &TemplateContext{
		BinaryPath:  "openrelik",
		Description: skillDescription(workers),
		Generated:   "2026-10-28",
		Workers:     workers,
	}

	t.Run("on PATH", func(t *testing.T) {
		got, err := renderSkill(data)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "skill_golden.md")
		if *updateGolden {
			if err := os.WriteFile(golden, got, 0644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("SKILL.md differs from %s; run 'go test ./cli -run TestRenderSkill -update' and review the diff.\nGot:\n%s", golden, got)
		}

		fields := frontmatter(t, string(got))
		for key := range fields {
			if key != "name" && key != "description" && key != "allowed-tools" {
				t.Errorf("unexpected frontmatter key %q", key)
			}
		}
		if fields["name"] != "openrelik" {
			t.Errorf("name = %q, want openrelik", fields["name"])
		}
		desc, err := strconv.Unquote(fields["description"])
		if err != nil || desc != data.Description {
			t.Errorf("description = %s, want %q quoted", fields["description"], data.Description)
		}
		tools := fields["allowed-tools"]
		if !strings.Contains(tools, "Bash(openrelik folder list:*)") || strings.Contains(tools, "Bash(openrelik:*)") ||
			strings.Contains(tools, "openrelik run") || strings.Contains(tools, "openrelik upload") {
			t.Errorf("allowed-tools = %q, want only read-only commands", tools)
		}
	})

	t.Run("not on PATH", func(t *testing.T) {
		abs := *data
		abs.BinaryPath = "/opt/openrelik/bin/openrelik"
		got, err := renderSkill(&abs)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := frontmatter(t, string(got))["allowed-tools"]; ok {
			t.Error("allowed-tools set for a binary that isn't on PATH")
		}
		if !strings.Contains(string(got), "Run the CLI as `/opt/openrelik/bin/openrelik`") {
			t.Errorf("expected the binary path in:\n%s", got)
		}
	})
}

func TestGenerateSkillFile(t *testing.T) {
	tempDir := t.TempDir()

	workers := []openrelik.Worker{
		{
			TaskName:    "openrelik-worker-strings.tasks.strings",
			DisplayName: "Strings",
			Description: "Extract strings from files.",
		},
	}

	if err := GenerateSkillFile(tempDir, workers); err != nil {
		t.Fatalf("GenerateSkillFile failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("failed to read generated SKILL.md: %v", err)
	}
	if !strings.Contains(string(content), "- **strings**: Extract strings from files.") {
		t.Errorf("expected the strings worker in:\n%s", content)
	}
}

func TestSkillInstallConfirmation(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	if err := config.SaveWorkersCache("", []openrelik.Worker{{TaskName: "openrelik-worker-strings.tasks.strings", DisplayName: "Strings"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		args        []string
		terminal    bool
		input       string
		wantErr     string
		wantInstall bool
	}{
		{name: "no terminal", wantErr: "No terminal to ask on; use --yes to confirm."},
		{name: "no terminal with --yes", args: []string{"--yes"}, wantInstall: true},
		{name: "terminal, confirmed", terminal: true, input: "y\n", wantInstall: true},
		{name: "terminal, declined", terminal: true, input: "n\n", wantErr: "cancelled; the skill was not installed"},
		{name: "unknown harness with a directory", args: []string{"--yes", "--harness", "foo"}, wantErr: "unknown harness: foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := stdinIsTerminal
			stdinIsTerminal = func(io.Reader) bool { return tt.terminal }
			defer func() { stdinIsTerminal = orig }()

			dir := t.TempDir()
			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetIn(strings.NewReader(tt.input))
			root.SetArgs(append([]string{"skill", "install", dir}, tt.args...))
			err := root.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			_, statErr := os.Stat(filepath.Join(dir, "openrelik", "SKILL.md"))
			if installed := statErr == nil; installed != tt.wantInstall {
				t.Errorf("installed = %v, want %v", installed, tt.wantInstall)
			}
		})
	}
}
