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

package util

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

// WorkflowTaskConfig represents the configuration for a single task in a workflow.
type WorkflowTaskConfig struct {
	Name        string      `json:"name"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Type        string      `json:"type"`
	Required    bool        `json:"required,omitempty"`
	Value       interface{} `json:"value"`
}

// WorkflowTask represents a single task or a group of tasks in a workflow.
type WorkflowTask struct {
	UUID        string               `json:"uuid"`
	TaskName    string               `json:"task_name"`
	QueueName   string               `json:"queue_name"`
	DisplayName string               `json:"display_name"`
	Description string               `json:"description"`
	TaskConfig  []WorkflowTaskConfig `json:"task_config"`
	Type        string               `json:"type"`
	Tasks       []WorkflowTask       `json:"tasks"` // For nested tasks if needed
}

// WorkflowSpecInner defines the structure of the workflow.
type WorkflowSpecInner struct {
	Type   string         `json:"type"`
	IsRoot bool           `json:"isRoot"`
	Tasks  []WorkflowTask `json:"tasks"`
}

// WorkflowSpec is the root object for a workflow specification.
type WorkflowSpec struct {
	Workflow WorkflowSpecInner `json:"workflow"`
}

// IsTerminalTaskStatus returns true for task statuses that represent a final state.
func IsTerminalTaskStatus(status string) bool {
	s := strings.ToLower(status)
	return s == "success" || s == "failure" || s == "complete" || s == "completed"
}

// GetOrCreateFolder finds or creates a folder with the given name in the root.
func GetOrCreateFolder(ctx context.Context, client *openrelik.Client, folderName string) (*openrelik.Folder, error) {
	folders, _, err := client.Folders().ListRootFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list root folders: %w", err)
	}

	for _, f := range folders {
		if f.DisplayName == folderName {
			return &f, nil
		}
	}

	// Create it
	folder, _, err := client.Folders().CreateRootFolder(ctx, folderName)
	if err != nil {
		return nil, fmt.Errorf("failed to create folder %s: %w", folderName, err)
	}

	return folder, nil
}

// WorkflowMonitor handles polling and UI for workflow status.
type WorkflowMonitor struct {
	client         *openrelik.Client
	workflow       *openrelik.Workflow
	taskShortNames []string
	taskUUIDs      []string
	uuidToShort    map[string]string
	interactive    bool
	showProgress   bool

	// Internal state
	taskStarted       map[string]time.Time
	taskEnded         map[string]time.Time
	taskStatusDisplay map[string]string
	mu                sync.Mutex
}

// NewWorkflowMonitor creates a new WorkflowMonitor.
func NewWorkflowMonitor(client *openrelik.Client, workflow *openrelik.Workflow, taskShortNames []string, taskUUIDs []string, uuidToShort map[string]string, interactive bool, showProgress bool) *WorkflowMonitor {
	return &WorkflowMonitor{
		client:            client,
		workflow:          workflow,
		taskShortNames:    taskShortNames,
		taskUUIDs:         taskUUIDs,
		uuidToShort:       uuidToShort,
		interactive:       interactive,
		showProgress:      showProgress,
		taskStarted:       make(map[string]time.Time),
		taskEnded:         make(map[string]time.Time),
		taskStatusDisplay: make(map[string]string),
	}
}

// Monitor polls the workflow status and updates the UI.
func (m *WorkflowMonitor) Monitor(ctx context.Context) (*openrelik.WorkflowStatus, error) {
	if m.interactive {
		return m.monitorInteractive(ctx)
	}
	return m.monitorNonInteractive(ctx)
}

func (m *WorkflowMonitor) monitorInteractive(ctx context.Context) (*openrelik.WorkflowStatus, error) {
	var lastStatus *openrelik.WorkflowStatus
	spinner := NewSpinner()
	spinner.Start()
	defer spinner.Stop()

	// Initial print of task lines to avoid overwriting previous output
	for _, shortName := range m.taskShortNames {
		fmt.Fprintf(os.Stderr, "%s·%s %-14s %spending%s\033[K\n", ColorDim, ColorReset, shortName, ColorDim, ColorReset)
	}

	stopUI := make(chan struct{})
	uiDone := make(chan struct{})

	// UI update goroutine
	go func() {
		defer close(uiDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.mu.Lock()
				s := spinner.Current()
				fmt.Fprintf(os.Stderr, "\033[%dA", len(m.taskShortNames))
				for i, shortName := range m.taskShortNames {
					uuid := m.taskUUIDs[i]
					ts := m.taskStatusDisplay[uuid]
					if ts == "" {
						ts = "PENDING"
					}
					if strings.EqualFold(ts, "FAILURE") {
						fmt.Fprintf(os.Stderr, "%s✖%s %-14s %sfailed%s\033[K\n", ColorRed, ColorReset, shortName, ColorRed, ColorReset)
					} else if IsTerminalTaskStatus(ts) {
						elapsed := m.taskEnded[uuid].Sub(m.taskStarted[uuid])
						fmt.Fprintf(os.Stderr, "%s✔%s %-14s %s%s%.1fs%s\033[K\n", ColorGreen, ColorReset, shortName, ColorDim, "[", elapsed.Seconds(), "]"+ColorReset)
					} else if strings.EqualFold(ts, "PENDING") {
						fmt.Fprintf(os.Stderr, "%s·%s %-14s %spending%s\033[K\n", ColorDim, ColorReset, shortName, ColorDim, ColorReset)
					} else {
						fmt.Fprintf(os.Stderr, "%s%s%s %-14s %s%s%s\033[K\n", ColorCyan, s, ColorReset, shortName, ColorCyan, strings.ToLower(ts), ColorReset)
					}
				}
				m.mu.Unlock()
			case <-stopUI:
				return
			}
		}
	}()

	// API polling loop
	var workflowFailed bool
	for {
		status, _, err := m.client.Workflows().Status(ctx, m.workflow.Folder.ID, m.workflow.ID)
		if err != nil {
			close(stopUI)
			<-uiDone
			return nil, fmt.Errorf("failed to get status for workflow %d: %w", m.workflow.ID, err)
		}
		lastStatus = status

		m.mu.Lock()
		now := time.Now()
		for _, task := range status.Tasks {
			ts := "PENDING"
			if task.StatusShort != nil {
				ts = *task.StatusShort
			}
			m.taskStatusDisplay[task.UUID] = ts
			if !strings.EqualFold(ts, "PENDING") && m.taskStarted[task.UUID].IsZero() {
				m.taskStarted[task.UUID] = now
			}
			if IsTerminalTaskStatus(ts) && m.taskEnded[task.UUID].IsZero() {
				m.taskEnded[task.UUID] = now
			}
		}
		done := IsTerminalTaskStatus(status.Status)
		workflowFailed = strings.EqualFold(status.Status, "FAILURE")
		m.mu.Unlock()

		if done {
			break
		}
		time.Sleep(2 * time.Second)
	}

	close(stopUI)
	<-uiDone

	// Final render
	fmt.Fprintf(os.Stderr, "\033[%dA", len(m.taskShortNames))
	for i, shortName := range m.taskShortNames {
		uuid := m.taskUUIDs[i]
		ts := m.taskStatusDisplay[uuid]
		if strings.EqualFold(ts, "FAILURE") {
			fmt.Fprintf(os.Stderr, "%s✖%s %-14s %sfailed%s\033[K\n", ColorRed, ColorReset, shortName, ColorRed, ColorReset)
		} else {
			elapsed := m.taskEnded[uuid].Sub(m.taskStarted[uuid])
			fmt.Fprintf(os.Stderr, "%s✔%s %-14s %s%s%.1fs%s\033[K\n", ColorGreen, ColorReset, shortName, ColorDim, "[", elapsed.Seconds(), "]"+ColorReset)
		}
	}

	if workflowFailed {
		return lastStatus, fmt.Errorf("workflow %d failed", m.workflow.ID)
	}

	return lastStatus, nil
}

// PrintSummary prints a summary of the workflow execution.
func (m *WorkflowMonitor) PrintSummary(startTime time.Time, totalUploaded, totalDownloaded int64) {
	if !m.showProgress {
		return
	}
	duration := time.Since(startTime).Round(time.Millisecond)
	sep := fmt.Sprintf(" %s·%s ", ColorDim, ColorReset)
	fmt.Fprintf(os.Stderr, "\nTasks: %d%sDuration: %s%sUploaded: %s%sDownloaded: %s\n",
		len(m.taskShortNames), sep,
		duration, sep,
		FormatBytes(totalUploaded), sep,
		FormatBytes(totalDownloaded))
}

func (m *WorkflowMonitor) monitorNonInteractive(ctx context.Context) (*openrelik.WorkflowStatus, error) {
	if m.showProgress {
		fmt.Fprintln(os.Stderr, "Running workflow...")
	}
	prevTaskStatuses := make(map[string]string) // UUID → status
	var lastStatus *openrelik.WorkflowStatus

	for {
		status, _, err := m.client.Workflows().Status(ctx, m.workflow.Folder.ID, m.workflow.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get status for workflow %d: %w", m.workflow.ID, err)
		}
		lastStatus = status

		now := time.Now()
		for _, task := range status.Tasks {
			ts := "PENDING"
			if task.StatusShort != nil {
				ts = *task.StatusShort
			}
			if !strings.EqualFold(ts, "PENDING") && m.taskStarted[task.UUID].IsZero() {
				m.taskStarted[task.UUID] = now
			}
			if IsTerminalTaskStatus(ts) && m.taskEnded[task.UUID].IsZero() {
				m.taskEnded[task.UUID] = now
			}
			if prevTaskStatuses[task.UUID] == ts || !IsTerminalTaskStatus(ts) {
				continue
			}
			shortName := m.uuidToShort[task.UUID]
			if shortName == "" {
				shortName = strings.ToLower(task.DisplayName)
			}
			elapsed := ""
			if !m.taskStarted[task.UUID].IsZero() {
				d := m.taskEnded[task.UUID].Sub(m.taskStarted[task.UUID])
				elapsed = fmt.Sprintf(" %.1fs", d.Seconds())
			}
			if m.showProgress {
				if strings.EqualFold(ts, "FAILURE") {
					fmt.Fprintf(os.Stderr, "✗ %-14s failed%s\n", shortName, elapsed)
				} else {
					fmt.Fprintf(os.Stderr, "✓ %-14s done%s\n", shortName, elapsed)
				}
			}
			prevTaskStatuses[task.UUID] = ts
		}

		done := IsTerminalTaskStatus(status.Status)
		if done {
			if strings.EqualFold(status.Status, "FAILURE") {
				return lastStatus, fmt.Errorf("workflow %d failed", m.workflow.ID)
			}
			break
		}
		time.Sleep(2 * time.Second)
	}
	return lastStatus, nil
}

// WorkflowMonitorMeta holds metadata for monitoring a workflow.
type WorkflowMonitorMeta struct {
	TaskShortNames []string
	TaskUUIDs      []string
	UUIDToShort    map[string]string

	// Resolved global flags (may be overridden in segments)
	DownloadPolicy   string
	OutputDir        string
	TaskFolders      bool
	UploadFolderID   int
	UploadFolderName string
}

// BuildWorkflowSpec constructs a workflow specification from command segments.
// It also identifies positional arguments (e.g. file IDs) and task-specific download preferences.
func BuildWorkflowSpec(runCmd *cobra.Command, segments [][]string, delimiter string, allWorkers []openrelik.Worker, createWorkerCmd func(openrelik.Worker, []openrelik.Worker) *cobra.Command) (WorkflowSpec, []string, map[string]bool, WorkflowMonitorMeta, error) {
	spec := WorkflowSpec{
		Workflow: WorkflowSpecInner{
			Type:   "chain",
			IsRoot: true,
			Tasks:  []WorkflowTask{},
		},
	}

	var positionalArgs []string
	var currentParent *WorkflowTask
	taskDownloadPrefs := make(map[string]bool)
	meta := WorkflowMonitorMeta{
		TaskShortNames: []string{},
		TaskUUIDs:      []string{},
		UUIDToShort:    make(map[string]string),
	}

	// Initialize with defaults from runCmd
	if runCmd != nil {
		meta.DownloadPolicy, _ = runCmd.Flags().GetString("download")
		if noDownload, _ := runCmd.Flags().GetBool("no-download"); noDownload {
			meta.DownloadPolicy = "none"
		}
		meta.OutputDir, _ = runCmd.Flags().GetString("output-dir")
		meta.TaskFolders, _ = runCmd.Flags().GetBool("task-folders")
		meta.UploadFolderID, _ = runCmd.Flags().GetInt("upload-folder-id")
		meta.UploadFolderName, _ = runCmd.Flags().GetString("upload-folder-name")
	}

	for _, segment := range segments {
		if len(segment) == 0 {
			continue
		}

		workerName := segment[0]
		currentWorker := FindWorker(workerName, allWorkers)
		if currentWorker == nil {
			return WorkflowSpec{}, nil, nil, WorkflowMonitorMeta{}, fmt.Errorf("unknown worker: %s", workerName)
		}

		meta.TaskShortNames = append(meta.TaskShortNames, segment[0])

		tempCmd := createWorkerCmd(*currentWorker, nil)
		tempCmd.DisableFlagParsing = false

		if runCmd != nil {
			tempCmd.PersistentFlags().AddFlagSet(runCmd.PersistentFlags())
		}

		tempCmd.SetArgs(segment[1:])
		tempCmd.RunE = func(c *cobra.Command, a []string) error { return nil }
		tempCmd.SetOut(io.Discard)
		tempCmd.SetErr(io.Discard)

		if err := tempCmd.Execute(); err != nil {
			return WorkflowSpec{}, nil, nil, WorkflowMonitorMeta{}, fmt.Errorf("failed to parse flags for worker %s: %w", workerName, err)
		}

		// Collect global overrides from this segment.
		if tempCmd.Flags().Changed("download") {
			meta.DownloadPolicy, _ = tempCmd.Flags().GetString("download")
		}
		if noDownload, _ := tempCmd.Flags().GetBool("no-download"); noDownload {
			meta.DownloadPolicy = "none"
		}
		if tempCmd.Flags().Changed("output-dir") {
			meta.OutputDir, _ = tempCmd.Flags().GetString("output-dir")
		}
		if taskFolders, _ := tempCmd.Flags().GetBool("task-folders"); taskFolders {
			meta.TaskFolders = true
		}
		if tempCmd.Flags().Changed("upload-folder-id") {
			meta.UploadFolderID, _ = tempCmd.Flags().GetInt("upload-folder-id")
		}
		if tempCmd.Flags().Changed("upload-folder-name") {
			meta.UploadFolderName, _ = tempCmd.Flags().GetString("upload-folder-name")
		}

		positionalArgs = append(positionalArgs, tempCmd.Flags().Args()...)

		taskConfig := []WorkflowTaskConfig{}
		for _, cfg := range currentWorker.TaskConfig {
			wCfg := WorkflowTaskConfig{
				Name:        cfg.Name,
				Label:       cfg.Label,
				Description: cfg.Description,
				Type:        cfg.Type,
				Required:    cfg.Required,
			}
			switch cfg.Type {
			case "string", "text", "textarea", "artifacts", "autocomplete", "select":
				wCfg.Value, _ = tempCmd.Flags().GetString(cfg.Name)
			case "boolean", "checkbox":
				wCfg.Value, _ = tempCmd.Flags().GetBool(cfg.Name)
			case "integer":
				wCfg.Value, _ = tempCmd.Flags().GetInt(cfg.Name)
			}
			taskConfig = append(taskConfig, wCfg)
		}

		uuid := GenerateUUID()
		meta.TaskUUIDs = append(meta.TaskUUIDs, uuid)
		meta.UUIDToShort[uuid] = segment[0]

		download, _ := tempCmd.Flags().GetBool("download-result")
		noDownload, _ := tempCmd.Flags().GetBool("no-download-result")
		if download {
			taskDownloadPrefs[uuid] = true
		} else if noDownload {
			taskDownloadPrefs[uuid] = false
		}

		newTask := WorkflowTask{
			UUID:        uuid,
			TaskName:    currentWorker.TaskName,
			QueueName:   currentWorker.QueueName,
			DisplayName: currentWorker.DisplayName,
			Description: currentWorker.Description,
			TaskConfig:  taskConfig,
			Type:        "task",
			Tasks:       []WorkflowTask{},
		}

		if delimiter == "--and" {
			spec.Workflow.Tasks = append(spec.Workflow.Tasks, newTask)
		} else {
			if currentParent == nil {
				spec.Workflow.Tasks = append(spec.Workflow.Tasks, newTask)
				currentParent = &spec.Workflow.Tasks[len(spec.Workflow.Tasks)-1]
			} else {
				currentParent.Tasks = append(currentParent.Tasks, newTask)
				currentParent = &currentParent.Tasks[len(currentParent.Tasks)-1]
			}
		}
	}

	return spec, positionalArgs, taskDownloadPrefs, meta, nil
}

// FindWorker identifies a worker by name or task name.
func FindWorker(name string, workers []openrelik.Worker) *openrelik.Worker {
	for _, w := range workers {
		use := w.TaskName
		parts := strings.Split(w.TaskName, ".")
		if len(parts) > 0 {
			use = parts[len(parts)-1]
		}
		if use == name || w.TaskName == name {
			return &w
		}
	}
	return nil
}

// FlattenTasks recursively flattens a tree of tasks into a linear slice.
func FlattenTasks(tasks []openrelik.Task) []openrelik.Task {
	var flattened []openrelik.Task
	for _, task := range tasks {
		flattened = append(flattened, task)
		if len(task.Tasks) > 0 {
			flattened = append(flattened, FlattenTasks(task.Tasks)...)
		}
	}
	return flattened
}

