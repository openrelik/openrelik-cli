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
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
)

//go:embed templates/skill.tmpl
var templateFS embed.FS

// skillNow returns the time the skill is generated; tests replace it.
var skillNow = time.Now

// maxSkillDescription is the longest description AgentSkills allows.
const maxSkillDescription = 1024

// skillDescriptionBase is the start of the skill's description, which the
// agent reads to decide when to use the skill. Worker names are appended.
const skillDescriptionBase = "Runs digital forensics processing on an OpenRelik server with the openrelik CLI: " +
	"finds, uploads and downloads evidence files (disk images, memory dumps, logs, archives), " +
	"runs workers on them alone, chained or in parallel, and reports results with SHA256 hashes. " +
	"Use when the user mentions OpenRelik, forensic evidence or artifacts, or one of its workers."

// SkillWorker represents a worker in the skill's list of workers.
type SkillWorker struct {
	CommandName string
	Description string
}

// readOnlyCommands are the commands the skill lets the agent run without
// asking, in harnesses that read allowed-tools. They only read from the
// server; uploads, runs, sharing and downloads still ask.
var readOnlyCommands = []string{
	"auth status", "case list", "file cat", "file info", "folder list",
	"folder search", "folder tree", "worker list", "workflow info",
	"workflow list", "workflow status", "workflow wait",
}

// allowedTools returns the allowed-tools value for readOnlyCommands.
func allowedTools() string {
	tools := make([]string, len(readOnlyCommands))
	for i, c := range readOnlyCommands {
		tools[i] = fmt.Sprintf("Bash(openrelik %s:*)", c)
	}
	return strings.Join(tools, ", ")
}

// TemplateContext holds the data required to render SKILL.md.
type TemplateContext struct {
	BinaryPath  string
	Description string
	Generated   string
	Workers     []SkillWorker
}

// getTemplateContext converts raw OpenRelik workers into our template-friendly structs.
func getTemplateContext(workers []openrelik.Worker) *TemplateContext {
	var skillWorkers []SkillWorker
	seen := map[string]bool{}
	for _, w := range workers {
		name := util.WorkerCommand(w)
		// 'run NAME' runs the first worker with the name, so only that
		// one is listed.
		if w.TaskName == "" || seen[name] {
			continue
		}
		seen[name] = true
		description := strings.Join(strings.Fields(w.Description), " ")
		if description == "" {
			description = w.DisplayName
		}
		skillWorkers = append(skillWorkers, SkillWorker{
			CommandName: name,
			Description: description,
		})
	}
	sort.Slice(skillWorkers, func(i, j int) bool {
		return skillWorkers[i].CommandName < skillWorkers[j].CommandName
	})
	return &TemplateContext{
		BinaryPath:  getBinaryPath(),
		Description: skillDescription(skillWorkers),
		Generated:   skillNow().UTC().Format("2006-01-02"),
		Workers:     skillWorkers,
	}
}

// skillDescription returns the skill's description, naming as many workers
// as fit in the length AgentSkills allows, and how many were left out.
func skillDescription(workers []SkillWorker) string {
	names := make([]string, len(workers))
	for i, w := range workers {
		names[i] = w.CommandName
	}
	if len(names) == 0 {
		return skillDescriptionBase
	}
	// Add names while they fit, leaving room to count the rest.
	desc := skillDescriptionBase + " Workers: "
	shown := 0
	for i, name := range names {
		next := desc
		if i > 0 {
			next += ", "
		}
		next += name
		end := "."
		if more := len(names) - i - 1; more > 0 {
			end = fmt.Sprintf(", and %d more.", more)
		}
		if len(next)+len(end) > maxSkillDescription {
			break
		}
		desc, shown = next, i+1
	}
	switch {
	case shown == 0:
		return skillDescriptionBase + fmt.Sprintf(" Workers: %d, listed in the skill.", len(names))
	case shown < len(names):
		return desc + fmt.Sprintf(", and %d more.", len(names)-shown)
	}
	return desc + "."
}

// getBinaryPath returns the path to the executable running the command.
func getBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "openrelik"
	}

	// Commands read best as plain "openrelik", which also survives the
	// binary being moved or upgraded, as long as it finds this binary.
	if onPath, err := exec.LookPath("openrelik"); err == nil {
		a, errA := os.Stat(onPath)
		b, errB := os.Stat(exe)
		if errA == nil && errB == nil && os.SameFile(a, b) {
			return "openrelik"
		}
	}

	// 'go run' and 'go test' run a temporary build that is gone once the
	// command ends, so there is no path to point at.
	if isTemporaryBuild(exe) {
		return "openrelik"
	}

	// Clean the path to make it absolute
	if absExe, err := filepath.Abs(exe); err == nil {
		return absExe
	}
	return exe
}

// isTemporaryBuild reports whether exe was built by 'go run' or 'go test'
// in Go's temporary build directory.
func isTemporaryBuild(exe string) bool {
	return strings.Contains(filepath.ToSlash(exe), "/go-build")
}

// renderSkill renders SKILL.md from data.
func renderSkill(data *TemplateContext) ([]byte, error) {
	tmpl, err := template.New("skill.tmpl").Funcs(template.FuncMap{
		// strconv.Quote's escapes are valid in a YAML double-quoted string.
		"quote":            strconv.Quote,
		"readOnlyCommands": allowedTools,
	}).ParseFS(templateFS, "templates/skill.tmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to parse skill template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute skill template: %w", err)
	}
	return buf.Bytes(), nil
}

// GenerateSkillFile creates the directory structure and outputs the compiled SKILL.md
func GenerateSkillFile(destDir string, workers []openrelik.Worker) error {
	content, err := renderSkill(getTemplateContext(workers))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create skill directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "SKILL.md"), content, 0644); err != nil {
		return fmt.Errorf("failed to write SKILL.md file: %w", err)
	}
	return nil
}
