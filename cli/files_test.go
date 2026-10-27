package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileDownloadCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/files/789" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 789, "display_name": "test.txt", "filesize": 13}`)
			return
		}
		if r.URL.Path == "/api/v1/files/789/download" {
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "hello world!!")
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

	t.Run("default current directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "openrelik-test-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get current directory: %v", err)
		}
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to change directory: %v", err)
		}
		defer os.Chdir(oldWd)

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"file", "download", "789"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, err := os.ReadFile("test.txt")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}
		if string(content) != "hello world!!" {
			t.Errorf("expected 'hello world!!', got %q", string(content))
		}
	})

	t.Run("specific directory", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "openrelik-test-*")
		defer os.RemoveAll(tmpDir)

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"file", "download", "789", tmpDir})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, _ := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
		if string(content) != "hello world!!" {
			t.Errorf("expected 'hello world!!', got %q", string(content))
		}
	})

	t.Run("specific file path", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "openrelik-test-*")
		defer os.RemoveAll(tmpDir)
		customPath := filepath.Join(tmpDir, "custom.bin")

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"file", "download", "789", customPath})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, _ := os.ReadFile(customPath)
		if string(content) != "hello world!!" {
			t.Errorf("expected 'hello world!!', got %q", string(content))
		}
	})

	t.Run("non-existent folder", func(t *testing.T) {
		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"file", "download", "789", "/non/existent/path/file.txt"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for non-existent folder")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("expected 'does not exist' error, got %v", err)
		}
	})

	t.Run("overwrite confirmation - yes", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "openrelik-overwrite-*")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("old content")
		tmpFile.Close()

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		// Mock "y" input
		root.SetIn(strings.NewReader("y\n"))
		root.SetArgs([]string{"file", "download", "789", tmpFile.Name()})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		content, _ := os.ReadFile(tmpFile.Name())
		if string(content) != "hello world!!" {
			t.Errorf("expected overwritten content 'hello world!!', got %q", string(content))
		}
	})

	t.Run("overwrite confirmation - empty (Enter)", func(t *testing.T) {
		tmpFile, _ := os.CreateTemp("", "openrelik-overwrite-*")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("old content")
		tmpFile.Close()

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		// Mock empty input (just newline)
		root.SetIn(strings.NewReader("\n"))
		root.SetArgs([]string{"file", "download", "789", tmpFile.Name()})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for cancelled download")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("expected 'cancelled' error, got %v", err)
		}

		content, _ := os.ReadFile(tmpFile.Name())
		if string(content) != "old content" {
			t.Errorf("expected original content 'old content', got %q", string(content))
		}
	})
}
