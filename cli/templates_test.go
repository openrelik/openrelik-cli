package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTemplateListCmd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows/templates/" {
			fmt.Fprintln(w, `[
				{"id": 1, "display_name": "Triage", "description": "Basic triage workflow"},
				{"id": 2, "display_name": "Full Analysis", "description": "Comprehensive analysis pipeline"}
			]`)
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

	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "human output",
			args:     []string{"template", "list"},
			expected: []string{"Triage", "Basic triage workflow", "Full Analysis"},
		},
		{
			name:     "json output",
			args:     []string{"template", "list", "--format", "json"},
			expected: []string{`"display_name"`, `"Triage"`, `"description"`},
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
			for _, want := range tt.expected {
				if !strings.Contains(output, want) {
					t.Errorf("expected output to contain %q, got %q", want, output)
				}
			}
		})
	}
}
