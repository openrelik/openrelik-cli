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
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/openrelik/openrelik-go-client"
)

//go:embed templates/skill.tmpl
var templateFS embed.FS

// SkillWorker represents a processed worker optimized for the AgentSkills template.
type SkillWorker struct {
	CommandName string
	TaskName    string
	DisplayName string
	Description string
	Config      []openrelik.TaskConfig
}

// TemplateContext holds the data required to render SKILL.md.
type TemplateContext struct {
	BinaryPath string
	Workers    []SkillWorker
}

// getTemplateContext converts raw OpenRelik workers into our template-friendly structs.
func getTemplateContext(workers []openrelik.Worker) *TemplateContext {
	var skillWorkers []SkillWorker
	for _, w := range workers {
		use := w.TaskName
		if use == "" {
			continue
		}
		parts := strings.Split(w.TaskName, ".")
		if len(parts) > 0 {
			use = parts[len(parts)-1]
		}

		skillWorkers = append(skillWorkers, SkillWorker{
			CommandName: use,
			TaskName:    w.TaskName,
			DisplayName: w.DisplayName,
			Description: w.Description,
			Config:      w.TaskConfig,
		})
	}
	return &TemplateContext{
		BinaryPath: getBinaryPath(),
		Workers:    skillWorkers,
	}
}

// getBinaryPath returns the path to the executable running the command.
func getBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "openrelik"
	}

	// Check if running in a test or under 'go run' (which builds a temp executable)
	base := filepath.Base(exe)
	isTemp := strings.Contains(exe, "go-build") || strings.HasPrefix(exe, os.TempDir())
	isTest := strings.HasSuffix(base, ".test") || strings.Contains(base, "test")

	if isTemp || isTest {
		// If running via go run in development, fallback to "go run <abs_main_path>"
		// if main.go exists in the current directory, or "openrelik" if not.
		if !isTest {
			if _, err := os.Stat("main.go"); err == nil {
				if absMain, err := filepath.Abs("main.go"); err == nil {
					return fmt.Sprintf("go run %s", absMain)
				}
			}
		}
		return "openrelik"
	}

	// Clean the path to make it absolute
	if absExe, err := filepath.Abs(exe); err == nil {
		return absExe
	}
	return exe
}

// GenerateSkillFile creates the directory structure and outputs the compiled SKILL.md
func GenerateSkillFile(destDir string, workers []openrelik.Worker) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create skill directory: %w", err)
	}

	tmpl, err := template.ParseFS(templateFS, "templates/skill.tmpl")
	if err != nil {
		return fmt.Errorf("failed to parse skill template: %w", err)
	}

	destPath := filepath.Join(destDir, "SKILL.md")
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create SKILL.md file: %w", err)
	}
	defer f.Close()

	data := getTemplateContext(workers)
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("failed to execute skill template: %w", err)
	}

	return nil
}
