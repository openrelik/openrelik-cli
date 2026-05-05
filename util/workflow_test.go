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
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	client, _ := openrelik.NewClient(server.URL, "test-key")
	ctx := context.Background()

	t.Run("Existing Folder", func(t *testing.T) {
		mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"folders": [{"id": 1, "display_name": "existing-folder"}]}`)
		})

		folder, err := GetOrCreateFolder(ctx, client, "existing-folder")
		if err != nil {
			t.Fatalf("GetOrCreateFolder failed: %v", err)
		}
		if folder.ID != 1 {
			t.Errorf("Expected ID 1, got %d", folder.ID)
		}
	})

	t.Run("Create New Folder", func(t *testing.T) {
		// New mux to avoid conflict
		mux := http.NewServeMux()
		server.Config.Handler = mux

		mux.HandleFunc("/api/v1/folders/all/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"folders": []}`)
		})
		mux.HandleFunc("/api/v1/folders/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id": 2, "display_name": "new-folder"}`)
			}
		})

		folder, err := GetOrCreateFolder(ctx, client, "new-folder")
		if err != nil {
			t.Fatalf("GetOrCreateFolder failed: %v", err)
		}
		if folder.ID != 2 {
			t.Errorf("Expected ID 2, got %d", folder.ID)
		}
	})
}

func TestWorkflowMonitor_NonInteractive(t *testing.T) {
	workflow := &openrelik.Workflow{
		ID: 100,
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

	t.Run("LongFlags", func(t *testing.T) {
		segments := [][]string{
			{"worker1", "--param1", "val1", "--output-dir", "/tmp/out", "--download", "all", "--task-folders"},
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
