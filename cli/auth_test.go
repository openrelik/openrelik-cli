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
		active := s.GetActiveServer()
		if active == nil || active.URL != "http://test-server" {
			t.Errorf("expected server URL %q, got %+v", "http://test-server", active)
		}
		if s.ActiveServer != "http://test-server" {
			t.Errorf("expected active server %q, got %q", "http://test-server", s.ActiveServer)
		}
		c, _ := config.LoadCredentials()
		if c.APIKeys["http://test-server"] != "test-api-key" {
			t.Errorf("expected API key %q, got %q", "test-api-key", c.APIKeys["http://test-server"])
		}
	})

	t.Run("LoginAddsNewServer", func(t *testing.T) {
		// Setup initial settings
		s := &config.Settings{
			ActiveServer: "http://initial-server",
			Servers:      []config.ServerConfig{{URL: "http://initial-server"}},
		}
		config.SaveSettings(s)

		root := NewRootCmd()
		out := new(bytes.Buffer)
		in := new(bytes.Buffer)
		root.SetOut(out)
		root.SetIn(in)

		in.WriteString("http://new-server\n")
		root.SetArgs([]string{"auth", "login"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		s, _ = config.LoadSettings()
		if len(s.Servers) != 2 {
			t.Errorf("expected 2 servers, got %d", len(s.Servers))
		}
		if s.ActiveServer != "http://new-server" {
			t.Errorf("expected active server to be http://new-server, got %s", s.ActiveServer)
		}
	})

	t.Run("SwitchServer", func(t *testing.T) {
		// Setup settings with two servers
		s := &config.Settings{
			ActiveServer: "http://server1",
			Servers: []config.ServerConfig{
				{URL: "http://server1"},
				{URL: "http://server2"},
			},
		}
		config.SaveSettings(s)

		root := NewRootCmd()
		out := new(bytes.Buffer)
		in := new(bytes.Buffer)
		root.SetOut(out)
		root.SetIn(in)

		// Select server 2 (index 2)
		in.WriteString("2\n")

		root.SetArgs([]string{"auth", "switch"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		if !bytes.Contains(out.Bytes(), []byte("Switched to http://server2")) {
			t.Errorf("expected output to contain success message, got %q", out.String())
		}

		// Verify active server was updated
		s, _ = config.LoadSettings()
		if s.ActiveServer != "http://server2" {
			t.Errorf("expected active server %q, got %q", "http://server2", s.ActiveServer)
		}
	})

	t.Run("AuthStatus", func(t *testing.T) {
		s := &config.Settings{
			ActiveServer: "http://test-server",
			Servers:      []config.ServerConfig{{URL: "http://test-server"}},
		}
		config.SaveSettings(s)

		root := NewRootCmd()
		out := new(bytes.Buffer)
		root.SetOut(out)

		root.SetArgs([]string{"auth", "status"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		expected := "Active server: http://test-server\n"
		if out.String() != expected {
			t.Errorf("expected output %q, got %q", expected, out.String())
		}
	})

	t.Run("AuthList", func(t *testing.T) {
		s := &config.Settings{
			ActiveServer: "http://server1",
			Servers: []config.ServerConfig{
				{URL: "http://server1"},
				{URL: "http://server2"},
			},
		}
		config.SaveSettings(s)

		root := NewRootCmd()
		out := new(bytes.Buffer)
		root.SetOut(out)

		root.SetArgs([]string{"auth", "list"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		expected := "Configured servers:\n- http://server1 [ACTIVE]\n- http://server2\n"
		if out.String() != expected {
			t.Errorf("expected output %q, got %q", expected, out.String())
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
