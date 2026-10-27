package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAuthStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/me/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("x-openrelik-refresh-token") != "good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"detail": "Invalid token"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": 4, "username": "analyst"}`)
	}))
	defer server.Close()

	tests := []struct {
		name        string
		active      string // active server in the settings file
		storedKey   string // key stored for the active server
		envServer   string
		envKey      string
		args        []string
		wantOut     string // exact output, with {settings}, {credentials} and {url} filled in
		wantErr     string
		notInOutput string
	}{
		{
			name:      "configured login",
			active:    server.URL,
			storedKey: "good-key",
			wantOut:   "Server   {url} (from {settings})\nAPI key  set (from {credentials})\nUser     analyst\n",
		},
		{
			name:      "environment overrides the active server",
			active:    "http://configured.example",
			storedKey: "other-key",
			envServer: server.URL,
			envKey:    "good-key",
			wantOut: "Server   {url} (from OPENRELIK_SERVER_URL)\nAPI key  set (from OPENRELIK_API_KEY)\nUser     analyst\n\n" +
				"The active server, http://configured.example, is not used while OPENRELIK_SERVER_URL is set.\n",
		},
		{
			name:      "rejected key",
			active:    server.URL,
			storedKey: "bad-key",
			wantOut:   "Server   {url} (from {settings})\nAPI key  set (from {credentials})\n",
			wantErr:   "the login to " + server.URL + " doesn't work",
		},
		{
			name:    "no key",
			active:  server.URL,
			wantOut: "Server   {url} (from {settings})\nAPI key  not set\n",
			wantErr: "no API key is set for " + server.URL,
		},
		{
			name:    "nothing configured",
			wantOut: "Server   not set\nAPI key  not set\n",
			wantErr: "no server is set",
		},
		{
			name:        "JSON never includes the key",
			active:      server.URL,
			storedKey:   "good-key",
			args:        []string{"--format", "json"},
			wantOut:     `"api_key_set": true`,
			notInOutput: "good-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetBaseDir(t.TempDir())
			defer config.SetBaseDir("")
			t.Setenv("OPENRELIK_SERVER_URL", tt.envServer)
			t.Setenv("OPENRELIK_API_KEY", tt.envKey)
			if tt.active != "" {
				config.SaveSettings(&config.Settings{ActiveServer: tt.active, Servers: []config.ServerConfig{{URL: tt.active}}})
			}
			if tt.storedKey != "" {
				config.SaveCredentials(&config.Credentials{APIKeys: map[string]string{tt.active: tt.storedKey}})
			}
			settingsPath, _ := config.SettingsPath()
			credentialsPath, _ := config.CredentialsPath()

			root := NewRootCmd()
			out := new(bytes.Buffer)
			root.SetOut(out)
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(append([]string{"auth", "status"}, tt.args...))
			err := root.Execute()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			want := strings.NewReplacer("{settings}", settingsPath, "{credentials}", credentialsPath, "{url}", server.URL).Replace(tt.wantOut)
			if tt.notInOutput != "" {
				if !strings.Contains(out.String(), want) || strings.Contains(out.String(), tt.notInOutput) {
					t.Errorf("output should contain %q and not %q:\n%s", want, tt.notInOutput, out.String())
				}
				return
			}
			if out.String() != want {
				t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
			}
		})
	}
}
