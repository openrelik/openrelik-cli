package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-go-client"
)

func TestFriendlyErrors(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/files/999999", "/api/v1/files/999999/download":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"detail": "File not found."}`)
		case "/api/v1/files/7":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"detail": "No access to this file."}`)
		case "/api/v1/files/8":
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/v1/workflows/5":
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `Internal Server Error`)
		case "/api/v1/files/9":
			fmt.Fprint(w, `{"id": "not a number"`)
		default: // including the token refresh
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	// A server that isn't listening.
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "not found", args: []string{"file", "info", "999999"}, want: "file 999999 not found"},
		{name: "forbidden", args: []string{"file", "info", "7"}, want: "permission denied: No access to this file"},
		{name: "unauthorized", args: []string{"file", "info", "8"}, want: "the server didn't accept the API key; check OPENRELIK_API_KEY"},
		{name: "server error", args: []string{"workflow", "info", "5"}, want: "the server failed to handle the request (500 Internal Server Error)"},
		{name: "bad response", args: []string{"file", "info", "9"}, want: "unexpected response from the server: "},
		{name: "repeated prefix is dropped", args: []string{"file", "cat", "999999"}, want: "file 999999 not found"},
		{name: "unreachable", args: []string{"--server", closedURL, "file", "info", "1"}, want: "could not reach " + closedURL + ": "},
		{name: "debug shows the raw error", args: []string{"--debug", "file", "info", "999999"}, want: "openrelik: GET " + server.URL + "/api/v1/files/999999: 404 Not Found: File not found."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to start with %q", err, tt.want)
			}
			if !strings.Contains(tt.want, "openrelik:") && err != nil && strings.Contains(err.Error(), "/api/v1/") {
				t.Errorf("error still shows the request: %v", err)
			}
		})
	}
}

func TestExitCodes(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/files/999999":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/files/7":
			w.WriteHeader(http.StatusForbidden)
		case "/api/v1/workflows/5":
			w.WriteHeader(http.StatusBadGateway)
		default: // including the token refresh
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	if err := config.SaveWorkersCache("", []openrelik.Worker{{TaskName: "openrelik-worker-strings.tasks.strings"}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		args    []string
		noCreds bool
		want    int
	}{
		{name: "unknown command", args: []string{"bogus"}, want: exitUsage},
		{name: "unknown subcommand", args: []string{"file", "bogus"}, want: exitUsage},
		{name: "unknown flag", args: []string{"file", "info", "1", "--bogus"}, want: exitUsage},
		{name: "missing argument", args: []string{"file", "info"}, want: exitUsage},
		{name: "invalid --format", args: []string{"--format", "yaml", "file", "info", "1"}, want: exitUsage},
		{name: "invalid value", args: []string{"folder", "list", "--limit", "-1"}, want: exitUsage},
		{name: "invalid ID", args: []string{"workflow", "info", "abc"}, want: exitUsage},
		{name: "required flag", args: []string{"workflow", "create", "1"}, want: exitUsage},
		{name: "removed short flag", args: []string{"workflow", "create", "-f", "1", "--file", "2", "--template", "3"}, want: exitUsage},
		{name: "exclusive flags", args: []string{"folder", "search", "x", "--latest", "--limit", "2"}, want: exitUsage},
		{name: "unknown worker flag", args: []string{"run", "--dry-run", "strings", "--bogus", "1"}, want: exitUsage},
		{name: "removed --no-download before the worker", args: []string{"run", "--no-download", "--dry-run", "strings", "1"}, want: exitUsage},
		{name: "removed --no-download after the worker", args: []string{"run", "--dry-run", "strings", "1", "--no-download"}, want: exitUsage},
		{name: "not found", args: []string{"file", "info", "999999"}, want: exitNotFound},
		{name: "key refused", args: []string{"file", "info", "1"}, want: exitAuth},
		{name: "forbidden", args: []string{"file", "info", "7"}, want: exitAuth},
		{name: "no credentials", args: []string{"file", "info", "1"}, noCreds: true, want: exitAuth},
		{name: "no credentials in auth status", args: []string{"auth", "status"}, noCreds: true, want: exitAuth},
		{name: "server error", args: []string{"workflow", "info", "5"}, want: exitUnavailable},
		{name: "unreachable", args: []string{"--server", closedURL, "file", "info", "1"}, want: exitUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.noCreds {
				t.Setenv("OPENRELIK_API_KEY", "")
				t.Setenv("OPENRELIK_SERVER_URL", "")
			} else {
				t.Setenv("OPENRELIK_API_KEY", "test-key")
				t.Setenv("OPENRELIK_SERVER_URL", server.URL)
			}
			root := NewRootCmd()
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := exitCode(err); got != tt.want {
				t.Errorf("exitCode(%q) = %d, want %d", err, got, tt.want)
			}
		})
	}

	if got := exitCode(notFoundError("no file named \"x\" in /Case A/")); got != exitNotFound {
		t.Errorf("exitCode(notFoundError) = %d, want %d", got, exitNotFound)
	}
}
