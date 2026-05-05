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

func TestFileListCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/folders/123/files/" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `[{"id": 1, "display_name": "file1.txt", "filesize": 1024}, {"id": 2, "display_name": "file2.txt", "filesize": 2048}]`)
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
		expectError    bool
	}{
		{
			name: "list files in folder",
			args: []string{"file", "list", "123"},
			expectedOutput: []string{
				"file1.txt",
				"1.0KB",
				"file2.txt",
				"2.0KB",
			},
		},
		{
			name:        "missing folder ID",
			args:        []string{"file", "list"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			buf := new(bytes.Buffer)
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(tt.args)

			err := root.Execute()
			if (err != nil) != tt.expectError {
				t.Fatalf("Execute() error = %v, expectError %v", err, tt.expectError)
			}

			if !tt.expectError {
				output := buf.String()
				for _, expected := range tt.expectedOutput {
					if !strings.Contains(output, expected) {
						t.Errorf("expected output to contain %q, but it was %q", expected, output)
					}
				}
			}
		})
	}
}

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

func TestFileUploadCmd(t *testing.T) {
	// Mock API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/files/upload" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"id": 101, "display_name": "upload.txt", "filesize": 13}`)
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

	t.Run("single file upload", func(t *testing.T) {
		// Create a dummy file to upload
		tmpFile, _ := os.CreateTemp("", "openrelik-upload-*")
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("hello upload!!")
		tmpFile.Close()

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"file", "upload", tmpFile.Name(), "123", "--chunk-size", "10"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "upload.txt") {
			t.Errorf("expected output to contain uploaded filename, but it was %q", output)
		}
		if !strings.Contains(output, "(ID 101)") {
			t.Errorf("expected output to contain uploaded file ID, but it was %q", output)
		}
	})

	t.Run("multiple file upload", func(t *testing.T) {
		tmpFile1, _ := os.CreateTemp("", "openrelik-upload-1-*")
		defer os.Remove(tmpFile1.Name())
		tmpFile1.WriteString("hello upload 1!!")
		tmpFile1.Close()

		tmpFile2, _ := os.CreateTemp("", "openrelik-upload-2-*")
		defer os.Remove(tmpFile2.Name())
		tmpFile2.WriteString("hello upload 2!!")
		tmpFile2.Close()

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetArgs([]string{"file", "upload", tmpFile1.Name(), tmpFile2.Name(), "123"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "upload.txt") {
			t.Errorf("expected output to contain uploaded filename, but it was %q", output)
		}
		// Each file gets its own confirmation line; mock returns ID 101 for every upload.
		count := strings.Count(output, "(ID 101)")
		if count < 2 {
			t.Errorf("expected at least 2 confirmation lines for ID 101, got %d. Output: %q", count, output)
		}
	})

	t.Run("directory upload error", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "openrelik-upload-dir-*")
		defer os.RemoveAll(tmpDir)

		root := NewRootCmd()
		buf := new(bytes.Buffer)
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"file", "upload", tmpDir, "123"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when uploading a directory")
		}
		if !strings.Contains(err.Error(), "is a directory, not a file") {
			t.Errorf("expected 'is a directory' error, got %v", err)
		}
	})
}
