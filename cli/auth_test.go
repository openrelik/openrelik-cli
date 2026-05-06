package cli

import (
	"bytes"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
)

func TestLoginCmd(t *testing.T) {
	// Setup temp config dir
	tmpDir := t.TempDir()
	config.SetBaseDir(tmpDir)
	defer config.SetBaseDir("")

	// Mock password reader
	originalPasswordReader := passwordReader
	passwordReader = func(fd int) ([]byte, error) {
		return []byte("test-api-key"), nil
	}
	defer func() { passwordReader = originalPasswordReader }()

	t.Run("SuccessfulLogin", func(t *testing.T) {
		root := NewRootCmd()
		out := new(bytes.Buffer)
		in := new(bytes.Buffer)
		root.SetOut(out)
		root.SetIn(in)

		// Provide input for server URL
		in.WriteString("http://test-server\n")

		root.SetArgs([]string{"auth", "login"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		if !bytes.Contains(out.Bytes(), []byte("Successfully logged in!")) {
			t.Errorf("expected output to contain success message, got %q", out.String())
		}

		// Verify config was saved
		s, _ := config.LoadSettings()
		if s.ServerURL != "http://test-server" {
			t.Errorf("expected server URL %q, got %q", "http://test-server", s.ServerURL)
		}
		c, _ := config.LoadCredentials()
		if c.APIKey != "test-api-key" {
			t.Errorf("expected API key %q, got %q", "test-api-key", c.APIKey)
		}
	})

	t.Run("MissingServerInput", func(t *testing.T) {
		root := NewRootCmd()
		in := new(bytes.Buffer)
		root.SetIn(in)
		// Empty input for server
		in.WriteString("\n")

		root.SetArgs([]string{"auth", "login"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for missing server input, got nil")
		}
		if err.Error() != "server URL is required" {
			t.Errorf("expected error %q, got %q", "server URL is required", err.Error())
		}
	})

	t.Run("MissingAPIKeyInput", func(t *testing.T) {
		// Mock empty password
		passwordReader = func(fd int) ([]byte, error) {
			return []byte(""), nil
		}

		root := NewRootCmd()
		in := new(bytes.Buffer)
		root.SetIn(in)
		in.WriteString("http://test-server\n")

		root.SetArgs([]string{"auth", "login"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for missing API key, got nil")
		}
		if err.Error() != "API key is required" {
			t.Errorf("expected error %q, got %q", "API key is required", err.Error())
		}
	})
}
