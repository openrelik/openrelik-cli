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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-go-client"
)

func TestGetTemplateContext(t *testing.T) {
	workers := []openrelik.Worker{
		{
			TaskName:    "openrelik-worker-strings.tasks.strings",
			DisplayName: "Strings",
			Description: "Extract strings from files.",
			TaskConfig: []openrelik.TaskConfig{
				{
					Name:        "min_len",
					Type:        "integer",
					Description: "Minimum string length",
					Required:    false,
				},
			},
		},
		{
			TaskName:    "test_worker",
			DisplayName: "Test Worker",
			Description: "A test worker with no config.",
		},
		{
			TaskName:    "",
			DisplayName: "Empty",
			Description: "Should be skipped.",
		},
	}

	ctx := getTemplateContext(workers)

	if len(ctx.Workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(ctx.Workers))
	}

	w1 := ctx.Workers[0]
	if w1.CommandName != "strings" {
		t.Errorf("expected CommandName 'strings', got '%s'", w1.CommandName)
	}
	if w1.DisplayName != "Strings" {
		t.Errorf("expected DisplayName 'Strings', got '%s'", w1.DisplayName)
	}
	if len(w1.Config) != 1 {
		t.Fatalf("expected 1 config param, got %d", len(w1.Config))
	}
	if w1.Config[0].Name != "min_len" {
		t.Errorf("expected config Name 'min_len', got '%s'", w1.Config[0].Name)
	}

	w2 := ctx.Workers[1]
	if w2.CommandName != "test_worker" {
		t.Errorf("expected CommandName 'test_worker', got '%s'", w2.CommandName)
	}
	if len(w2.Config) != 0 {
		t.Errorf("expected 0 config params, got %d", len(w2.Config))
	}
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

	err := GenerateSkillFile(tempDir, workers)
	if err != nil {
		t.Fatalf("GenerateSkillFile failed: %v", err)
	}

	skillFile := filepath.Join(tempDir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		t.Fatalf("expected file %s to exist", skillFile)
	}

	contentBytes, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("failed to read generated SKILL.md: %v", err)
	}

	content := string(contentBytes)

	// Check for dynamic content
	if !strings.Contains(content, "### Strings") {
		t.Errorf("expected content to contain '### Strings'")
	}
	if !strings.Contains(content, "run strings") {
		t.Errorf("expected content to contain 'run strings'")
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
