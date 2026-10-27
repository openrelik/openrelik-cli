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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

func TestIsTerminalTaskStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"SUCCESS", true},
		{"FAILURE", true},
		{"COMPLETE", true},
		{"COMPLETED", true},
		{"success", true},
		{"failure", true},
		{"PENDING", false},
		{"RUNNING", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsTerminalTaskStatus(tt.status); got != tt.want {
			t.Errorf("IsTerminalTaskStatus(%q) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestGetOrCreateFolder(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		folders string // JSON list of root folders; the current user has ID 7
		wantID  int
		created bool
	}{
		{name: "existing folder", folders: `{"id": 1, "display_name": "uploads", "user": {"id": 7}}`, wantID: 1},
		{name: "creates when missing", folders: `{"id": 1, "display_name": "other", "user": {"id": 7}}`, wantID: 2, created: true},
		{name: "ignores folders shared by others", folders: `{"id": 1, "display_name": "uploads", "user": {"id": 8}}`, wantID: 2, created: true},
		{
			name:    "duplicates use the most recently updated",
			folders: `{"id": 1, "display_name": "uploads", "user": {"id": 7}, "updated_at": "2026-01-01T00:00:00Z"}, {"id": 3, "display_name": "uploads", "user": {"id": 7}, "updated_at": "2026-02-01T00:00:00Z"}`,
			wantID:  3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			server := httptest.NewServer(mux)
			defer server.Close()
			client, _ := openrelik.NewClient(server.URL, "test-key")

			mux.HandleFunc("/api/v1/users/me/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 7}`)
			})
			mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"folders": [%s], "page": 1, "page_size": 100, "total_count": 0}`, tt.folders)
			})
			created := false
			mux.HandleFunc("/api/v1/folders/", func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					created = true
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id": 2, "display_name": "uploads"}`)
				}
			})

			folder, err := GetOrCreateFolder(ctx, client, "uploads")
			if err != nil {
				t.Fatalf("GetOrCreateFolder failed: %v", err)
			}
			if folder.ID != tt.wantID {
				t.Errorf("got folder %d, want %d", folder.ID, tt.wantID)
			}
			if created != tt.created {
				t.Errorf("created = %v, want %v", created, tt.created)
			}
		})
	}
}

func TestWorkflowMonitor_NonInteractive(t *testing.T) {
	workflow := &openrelik.Workflow{
		ID:     100,
		Folder: openrelik.Folder{ID: 1},
	}

	t.Run("Success", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")

		mux.HandleFunc("/api/v1/folders/1/workflows/100/status/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"status": "SUCCESS",
				"tasks": [
					{"uuid": "task1", "display_name": "Task 1", "status_short": "SUCCESS"}
				]
			}`)
		})

		m := NewWorkflowMonitor(client, workflow, []string{"Task 1"}, []string{"task1"}, map[string]string{"task1": "Task 1"}, false, false)
		status, err := m.Monitor(context.Background())
		if err != nil {
			t.Fatalf("Monitor failed: %v", err)
		}
		if status.Status != "SUCCESS" {
			t.Errorf("Expected status SUCCESS, got %s", status.Status)
		}
	})

	t.Run("Failure", func(t *testing.T) {
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		defer server.Close()
		client, _ := openrelik.NewClient(server.URL, "test-key")

		mux.HandleFunc("/api/v1/folders/1/workflows/100/status/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"status": "FAILURE",
				"tasks": [
					{"uuid": "task1", "display_name": "Task 1", "status_short": "FAILURE"}
				]
			}`)
		})

		m := NewWorkflowMonitor(client, workflow, []string{"Task 1"}, []string{"task1"}, map[string]string{"task1": "Task 1"}, false, false)
		status, err := m.Monitor(context.Background())
		if err == nil {
			t.Fatal("Expected error for failed workflow, got nil")
		}
		if status == nil {
			t.Fatalf("Expected non-nil status, got nil (err: %v)", err)
		}
		if status.Status != "FAILURE" {
			t.Errorf("Expected status FAILURE, got %s", status.Status)
		}
	})
}

func TestWorkflowMonitor_PrintSummary(t *testing.T) {
	m := &WorkflowMonitor{showProgress: true, taskShortNames: []string{"T1", "T2"}}
	// This mainly tests that it doesn't crash
	m.PrintSummary(time.Now().Add(-10*time.Second), 1024, 2048)

	m.showProgress = false
	m.PrintSummary(time.Now().Add(-10*time.Second), 1024, 2048)
}

func TestBuildWorkflowSpec_Flags(t *testing.T) {
	allWorkers := []openrelik.Worker{
		{
			TaskName:    "worker1",
			DisplayName: "Worker 1",
			TaskConfig: []openrelik.TaskConfig{
				{Name: "param1", Type: "string"},
			},
		},
	}

	createWorkerCmd := func(worker openrelik.Worker, _ []openrelik.Worker) *cobra.Command {
		cmd := &cobra.Command{
			Use: worker.TaskName,
		}
		for _, cfg := range worker.TaskConfig {
			cmd.Flags().String(cfg.Name, "", "")
		}
		cmd.Flags().Bool("download-result", false, "")
		cmd.Flags().Bool("no-download-result", false, "")
		return cmd
	}

	runCmd := &cobra.Command{Use: "run"}
	runCmd.PersistentFlags().StringP("output-dir", "o", ".", "")
	runCmd.PersistentFlags().String("download", "final", "")
	runCmd.PersistentFlags().Bool("no-download", false, "")
	runCmd.PersistentFlags().Bool("task-folders", false, "")
	runCmd.PersistentFlags().Bool("dry-run", false, "")

	t.Run("LongFlags", func(t *testing.T) {
		segments := [][]string{
			{"worker1", "--param1", "val1", "--output-dir", "/tmp/out", "--download", "all", "--task-folders", "--dry-run"},
		}

		spec, _, _, meta, err := BuildWorkflowSpec(runCmd, segments, "--then", allWorkers, createWorkerCmd)
		if err != nil {
			t.Fatalf("BuildWorkflowSpec failed: %v", err)
		}

		if meta.OutputDir != "/tmp/out" {
			t.Errorf("Expected OutputDir /tmp/out, got %s", meta.OutputDir)
		}
		if meta.DownloadPolicy != "all" {
			t.Errorf("Expected DownloadPolicy all, got %s", meta.DownloadPolicy)
		}
		if !meta.TaskFolders {
			t.Errorf("Expected TaskFolders true, got false")
		}
		if !meta.DryRun {
			t.Errorf("Expected DryRun true, got false")
		}

		if len(spec.Workflow.Tasks) != 1 {
			t.Fatalf("Expected 1 task, got %d", len(spec.Workflow.Tasks))
		}
		task := spec.Workflow.Tasks[0]
		foundParam := false
		for _, cfg := range task.TaskConfig {
			if cfg.Name == "param1" {
				if cfg.Value != "val1" {
					t.Errorf("Expected param1 val1, got %v", cfg.Value)
				}
				foundParam = true
			}
		}
		if !foundParam {
			t.Errorf("param1 not found in task config")
		}
	})

	t.Run("NoDownload", func(t *testing.T) {
		segments := [][]string{
			{"worker1", "--no-download"},
		}
		_, _, _, meta, err := BuildWorkflowSpec(runCmd, segments, "--then", allWorkers, createWorkerCmd)
		if err != nil {
			t.Fatalf("BuildWorkflowSpec failed: %v", err)
		}
		if meta.DownloadPolicy != "none" {
			t.Errorf("Expected DownloadPolicy none, got %s", meta.DownloadPolicy)
		}
	})

	t.Run("ShorthandOutputDir", func(t *testing.T) {
		segments := [][]string{
			{"worker1", "-o/tmp/shorthand"},
		}
		_, _, _, meta, err := BuildWorkflowSpec(runCmd, segments, "--then", allWorkers, createWorkerCmd)
		if err != nil {
			t.Fatalf("BuildWorkflowSpec failed: %v", err)
		}
		if meta.OutputDir != "/tmp/shorthand" {
			t.Errorf("Expected OutputDir /tmp/shorthand, got %s", meta.OutputDir)
		}
	})
}
