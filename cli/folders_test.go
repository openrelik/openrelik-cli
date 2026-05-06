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

func TestFolderListCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/folders/all/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"folders": [{"id": 1, "display_name": "Root 1", "uuid": "uuid1"}, {"id": 2, "display_name": "Root 2", "uuid": "uuid2"}], "page": 1, "page_size": 10, "total_count": 2}`)
			return
		}
		if r.URL.Path == "/api/v1/folders/1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `[{"id": 3, "display_name": "Sub 1", "uuid": "uuid3"}]`)
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
		name           string
		args           []string
		expectedOutput []string
	}{
		{
			name: "list root folders",
			args: []string{"folder", "list"},
			expectedOutput: []string{
				"Root 1",
				"Root 2",
			},
		},
		{
			name: "list subfolders",
			args: []string{"folder", "list", "1"},
			expectedOutput: []string{
				"Sub 1",
			},
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
			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("expected output to contain %q, but it was %q", expected, output)
				}
			}
		})
	}
}

func TestFolderCreateCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 100, "display_name": "New Root", "uuid": "uuid100"}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/folders/1/folders/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 200, "display_name": "New Sub", "uuid": "uuid200"}`)
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
		name           string
		args           []string
		expectedOutput []string
	}{
		{
			name: "create root folder",
			args: []string{"folder", "create", "New Root"},
			expectedOutput: []string{
				`"New Root" created (ID 100)`,
			},
		},
		{
			name: "create subfolder",
			args: []string{"folder", "create", "New Sub", "--parent", "1"},
			expectedOutput: []string{
				`"New Sub" created (ID 200)`,
			},
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
			for _, expected := range tt.expectedOutput {
				if !strings.Contains(output, expected) {
					t.Errorf("expected output to contain %q, but it was %q", expected, output)
				}
			}
		})
	}
}
