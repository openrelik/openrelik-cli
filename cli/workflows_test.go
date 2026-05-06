package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
