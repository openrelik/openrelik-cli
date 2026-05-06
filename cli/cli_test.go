package cli

import (
	"os"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
)

func TestNewClient(t *testing.T) {
	// Setup temp config dir
	tmpDir := t.TempDir()
	config.SetBaseDir(tmpDir)
	defer config.SetBaseDir("")

	// Helper to reset globals
	resetGlobals := func() {
		serverURL = ""
		apiKey = ""
		os.Unsetenv("OPENRELIK_SERVER_URL")
		os.Unsetenv("OPENRELIK_API_KEY")
	}

	t.Run("Flags", func(t *testing.T) {
		resetGlobals()
		defer resetGlobals()
		serverURL = "http://flag-server"
		apiKey = "flag-key"

		client, err := newClient()
		if err != nil {
			t.Fatalf("newClient failed: %v", err)
		}
		if client == nil {
			t.Fatal("expected client, got nil")
		}
	})

	t.Run("EnvVars", func(t *testing.T) {
		resetGlobals()
		defer resetGlobals()
		os.Setenv("OPENRELIK_SERVER_URL", "http://env-server")
		os.Setenv("OPENRELIK_API_KEY", "env-key")

		client, err := newClient()
		if err != nil {
			t.Fatalf("newClient failed: %v", err)
		}
		if client == nil {
			t.Fatal("expected client, got nil")
		}
	})

	t.Run("ConfigFallback", func(t *testing.T) {
		resetGlobals()
		defer resetGlobals()

		config.SaveSettings(&config.Settings{ServerURL: "http://config-server"})
		config.SaveCredentials(&config.Credentials{APIKey: "config-key"})

		client, err := newClient()
		if err != nil {
			t.Fatalf("newClient failed: %v", err)
		}
		if client == nil {
			t.Fatal("expected client, got nil")
		}
	})

	t.Run("MissingServer", func(t *testing.T) {
		resetGlobals()
		defer resetGlobals()
		apiKey = "some-key"
		// Remove config files
		dir, _ := config.GetConfigDir()
		os.RemoveAll(dir)

		_, err := newClient()
		if err == nil {
			t.Error("expected error for missing server URL, got nil")
		}
	})

	t.Run("MissingKey", func(t *testing.T) {
		resetGlobals()
		defer resetGlobals()
		serverURL = "http://some-server"
		// Remove config files
		dir, _ := config.GetConfigDir()
		os.RemoveAll(dir)

		_, err := newClient()
		if err == nil {
			t.Error("expected error for missing API key, got nil")
		}
	})
}
