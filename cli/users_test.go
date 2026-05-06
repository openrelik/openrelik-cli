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

func TestMeCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/users/me/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 1, "username": "testuser", "display_name": "Test User", "is_admin": true}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Setup command
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	// Set API key and server URL via environment variables since flags were removed
	os.Setenv("OPENRELIK_API_KEY", "test-key")
	os.Setenv("OPENRELIK_SERVER_URL", server.URL)
	defer func() {
		os.Unsetenv("OPENRELIK_API_KEY")
		os.Unsetenv("OPENRELIK_SERVER_URL")
	}()

	// Set flags (server flag removed)
	root.SetArgs([]string{"user", "me"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	output := buf.String()
	expectedFields := []string{
		"ID            1",
		"Username      testuser",
		"Display Name  Test User",
		"Admin         true",
	}

	for _, field := range expectedFields {
		if !strings.Contains(output, field) {
			t.Errorf("expected output to contain %q, but it was %q", field, output)
		}
	}
}

func TestMeCmdJSON(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/users/me/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 1, "username": "testuser", "display_name": "Test User", "is_admin": true}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Setup command
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)

	os.Setenv("OPENRELIK_API_KEY", "test-key")
	os.Setenv("OPENRELIK_SERVER_URL", server.URL)
	defer func() {
		os.Unsetenv("OPENRELIK_API_KEY")
		os.Unsetenv("OPENRELIK_SERVER_URL")
	}()

	// Set flags to request JSON output (server flag removed)
	root.SetArgs([]string{"--format", "json", "user", "me"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"username": "testuser"`) {
		t.Errorf("expected output to contain JSON username, but it was %q", output)
	}
	if !strings.Contains(output, "{") || !strings.Contains(output, "}") {
		t.Errorf("output does not look like JSON: %q", output)
	}
}
