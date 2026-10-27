package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
)

// newMeServer is a mock API whose /users/me endpoint accepts only
// acceptedKey, as the user "analyst".
func newMeServer(t *testing.T, acceptedKey string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/me/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("x-openrelik-refresh-token") != acceptedKey {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"detail": "Invalid token"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id": 4, "username": "analyst"}`)
	}))
	t.Cleanup(server.Close)
	return server
}

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
	// The prompts need a terminal; stdin here is a buffer.
	originalStdinIsTerminal := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	defer func() { stdinIsTerminal = originalStdinIsTerminal }()
	testServer := newMeServer(t, "test-api-key").URL
	newServer := newMeServer(t, "test-api-key").URL

	t.Run("SuccessfulLogin", func(t *testing.T) {
		root := NewRootCmd()
		out := new(bytes.Buffer)
		in := new(bytes.Buffer)
		root.SetOut(out)
		root.SetIn(in)

		// Provide input for server URL
		in.WriteString(testServer + "\n")

		root.SetArgs([]string{"auth", "login"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		if !bytes.Contains(out.Bytes(), []byte("Logged in to "+testServer+" as analyst\n")) {
			t.Errorf("expected output to contain success message, got %q", out.String())
		}

		// Verify config was saved
		s, _ := config.LoadSettings()
		active := s.GetActiveServer()
		if active == nil || active.URL != testServer {
			t.Errorf("expected server URL %q, got %+v", testServer, active)
		}
		if s.ActiveServer != testServer {
			t.Errorf("expected active server %q, got %q", testServer, s.ActiveServer)
		}
		c, _ := config.LoadCredentials()
		if c.APIKeys[testServer] != "test-api-key" {
			t.Errorf("expected API key %q, got %q", "test-api-key", c.APIKeys[testServer])
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

		in.WriteString(newServer + "\n")
		root.SetArgs([]string{"auth", "login"})

		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}

		s, _ = config.LoadSettings()
		if len(s.Servers) != 2 {
			t.Errorf("expected 2 servers, got %d", len(s.Servers))
		}
		if s.ActiveServer != newServer {
			t.Errorf("expected active server to be %s, got %s", newServer, s.ActiveServer)
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

		expected := "#  SERVER          ACTIVE  API KEY\n1  http://server1  yes     not saved\n2  http://server2          not saved\n"
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
	server := newMeServer(t, "good-key")

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

// TestLoginFailureSavesNothing checks that a login the server doesn't
// accept, or a server that can't be reached, leaves the existing
// credentials alone.
func TestLoginFailureSavesNothing(t *testing.T) {
	server := newMeServer(t, "good-key")
	unreachable := httptest.NewServer(http.NotFoundHandler())
	unreachable.Close()

	tests := []struct {
		name    string
		url     string
		key     string
		wantErr string
	}{
		{name: "rejected key", url: server.URL, key: "bad-key", wantErr: "could not log in to " + server.URL},
		{name: "unreachable server", url: unreachable.URL, key: "good-key", wantErr: "could not log in to " + unreachable.URL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetBaseDir(t.TempDir())
			defer config.SetBaseDir("")
			// A working login that must survive the failed one.
			config.SaveSettings(&config.Settings{ActiveServer: "http://working", Servers: []config.ServerConfig{{URL: "http://working"}}})
			config.SaveCredentials(&config.Credentials{APIKeys: map[string]string{"http://working": "working-key"}})

			orig := passwordReader
			passwordReader = func(int) ([]byte, error) { return []byte(tt.key), nil }
			defer func() { passwordReader = orig }()
			origTerminal := stdinIsTerminal
			stdinIsTerminal = func(io.Reader) bool { return true }
			defer func() { stdinIsTerminal = origTerminal }()

			root := NewRootCmd()
			out := new(bytes.Buffer)
			root.SetOut(out)
			root.SetErr(new(bytes.Buffer))
			root.SetIn(strings.NewReader(tt.url + "\n"))
			root.SetArgs([]string{"auth", "login"})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "Nothing was saved.") {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
			if strings.Contains(out.String(), "Logged in") {
				t.Errorf("reported a login that failed: %q", out.String())
			}

			s, _ := config.LoadSettings()
			c, _ := config.LoadCredentials()
			if s.ActiveServer != "http://working" || len(s.Servers) != 1 {
				t.Errorf("settings changed: %+v", s)
			}
			if len(c.APIKeys) != 1 || c.APIKeys["http://working"] != "working-key" {
				t.Errorf("credentials changed: %v", c.APIKeys)
			}
		})
	}
}

// runAuth runs an auth command with input on stdin, which counts as a
// terminal if terminal is set, and returns stdout, stderr and the error.
func runAuth(t *testing.T, input string, terminal bool, args ...string) (string, string, error) {
	t.Helper()
	orig := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return terminal }
	defer func() { stdinIsTerminal = orig }()
	root := NewRootCmd()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(strings.NewReader(input))
	root.SetArgs(append([]string{"auth"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestLoginNonInteractive(t *testing.T) {
	server := newMeServer(t, "good-key")
	orig := passwordReader
	passwordReader = func(int) ([]byte, error) {
		t.Error("the key was read from the terminal")
		return nil, nil
	}
	defer func() { passwordReader = orig }()

	tests := []struct {
		name       string
		args       []string
		input      string
		wantServer string // saved active server; empty if the login should fail
		wantErr    string
	}{
		{name: "key on stdin", args: []string{server.URL, "--api-key-stdin"}, input: "good-key\n", wantServer: server.URL},
		{name: "trailing slash is dropped", args: []string{server.URL + "/", "--api-key-stdin"}, input: "good-key", wantServer: server.URL},
		{name: "stdin key needs SERVER", args: []string{"--api-key-stdin"}, input: "good-key\n", wantErr: "--api-key-stdin needs SERVER"},
		{name: "empty key on stdin", args: []string{server.URL, "--api-key-stdin"}, input: "\n", wantErr: "API key is required"},
		{name: "no terminal for the key", args: []string{server.URL}, wantErr: "no terminal to read the API key from; pipe it in with --api-key-stdin"},
		{name: "invalid URL", args: []string{"openrelik.example.org", "--api-key-stdin"}, input: "good-key", wantErr: `invalid server URL "openrelik.example.org"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetBaseDir(t.TempDir())
			defer config.SetBaseDir("")
			t.Setenv("OPENRELIK_SERVER_URL", "")
			stdout, _, err := runAuth(t, tt.input, false, append([]string{"login"}, tt.args...)...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				if _, err := config.LoadSettings(); err == nil {
					t.Error("settings were saved despite the error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if want := "Logged in to " + tt.wantServer + " as analyst\n"; stdout != want {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}
			s, _ := config.LoadSettings()
			c, _ := config.LoadCredentials()
			if s.ActiveServer != tt.wantServer || c.APIKeys[tt.wantServer] != "good-key" {
				t.Errorf("saved active server %q and keys %v", s.ActiveServer, c.APIKeys)
			}
		})
	}
}

func TestAuthSwitchAndLogout(t *testing.T) {
	const a, b, c = "http://a.example", "http://b.example", "http://c.example"

	tests := []struct {
		name       string
		args       []string
		input      string
		terminal   bool
		wantOut    string
		wantStderr string
		wantErr    string
		wantActive string
		wantSaved  []string // servers left in the settings, with their keys
	}{
		{name: "switch by number", args: []string{"switch", "3"}, wantOut: "Switched to " + c + "\n", wantActive: c, wantSaved: []string{a, b, c}},
		{name: "switch by URL", args: []string{"switch", b + "/"}, wantOut: "Switched to " + b + "\n", wantActive: b, wantSaved: []string{a, b, c}},
		{name: "switch to unknown server", args: []string{"switch", "http://d.example"}, wantErr: `no saved server "http://d.example"`, wantActive: a, wantSaved: []string{a, b, c}},
		{name: "switch to invalid number", args: []string{"switch", "4"}, wantErr: "invalid choice 4: there are 3 saved servers", wantActive: a, wantSaved: []string{a, b, c}},
		{name: "switch without a terminal", args: []string{"switch"}, wantErr: "give SERVER", wantActive: a, wantSaved: []string{a, b, c}},
		{name: "switch interactively", args: []string{"switch"}, input: "2\n", terminal: true, wantOut: "Switched to " + b + "\n",
			wantStderr: "Select a server:\n1) " + a + " [ACTIVE]\n2) " + b + "\n3) " + c + "\nEnter number: ", wantActive: b, wantSaved: []string{a, b, c}},
		{name: "logout of a server", args: []string{"logout", "2"}, wantOut: "Logged out of " + b + "\n", wantActive: a, wantSaved: []string{a, c}},
		{name: "logout of the active server", args: []string{"logout"}, wantOut: "Logged out of " + a + "\n",
			wantStderr: "No server is active now; choose one with 'openrelik auth switch'.\n", wantActive: "", wantSaved: []string{b, c}},
		{name: "logout of unknown server", args: []string{"logout", "http://d.example"}, wantErr: `no saved server "http://d.example"`, wantActive: a, wantSaved: []string{a, b, c}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetBaseDir(t.TempDir())
			defer config.SetBaseDir("")
			t.Setenv("OPENRELIK_SERVER_URL", "")
			config.SaveSettings(&config.Settings{ActiveServer: a, Servers: []config.ServerConfig{{URL: a}, {URL: b}, {URL: c}}})
			config.SaveCredentials(&config.Credentials{APIKeys: map[string]string{a: "key-a", b: "key-b", c: "key-c"}})

			stdout, stderr, err := runAuth(t, tt.input, tt.terminal, tt.args...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if stdout != tt.wantOut {
				t.Errorf("stdout = %q, want %q", stdout, tt.wantOut)
			}
			if stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.wantStderr)
			}

			s, _ := config.LoadSettings()
			creds, _ := config.LoadCredentials()
			if s.ActiveServer != tt.wantActive {
				t.Errorf("active server = %q, want %q", s.ActiveServer, tt.wantActive)
			}
			var saved []string
			for _, srv := range s.Servers {
				saved = append(saved, srv.URL)
			}
			if strings.Join(saved, ",") != strings.Join(tt.wantSaved, ",") || len(creds.APIKeys) != len(tt.wantSaved) {
				t.Errorf("saved servers %v and keys %v, want %v", saved, creds.APIKeys, tt.wantSaved)
			}
			for _, srv := range tt.wantSaved {
				if creds.APIKeys[srv] == "" {
					t.Errorf("key for %s was removed", srv)
				}
			}
		})
	}
}

func TestAuthListAndOverride(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	const a, b = "http://a.example", "http://b.example"
	config.SaveSettings(&config.Settings{ActiveServer: a, Servers: []config.ServerConfig{{URL: a}, {URL: b}}})
	config.SaveCredentials(&config.Credentials{APIKeys: map[string]string{a: "key-a"}})

	t.Run("JSON", func(t *testing.T) {
		t.Setenv("OPENRELIK_SERVER_URL", "")
		stdout, _, err := runAuth(t, "", false, "list", "--format", "json")
		if err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}
		want := `[{"number":1,"url":"http://a.example","active":true,"api_key_saved":true},{"number":2,"url":"http://b.example","active":false,"api_key_saved":false}]`
		if strings.Join(strings.Fields(stdout), "") != want {
			t.Errorf("stdout = %s, want %s", stdout, want)
		}
	})

	t.Run("override is shown", func(t *testing.T) {
		t.Setenv("OPENRELIK_SERVER_URL", "http://env.example")
		stdout, _, err := runAuth(t, "", false, "list")
		if err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}
		if !strings.HasSuffix(stdout, "\nCommands use http://env.example instead while OPENRELIK_SERVER_URL is set.\n") {
			t.Errorf("expected the override to be shown:\n%s", stdout)
		}
		_, stderr, err := runAuth(t, "", false, "switch", "2")
		if err != nil || stderr != "Note: commands use http://env.example instead while OPENRELIK_SERVER_URL is set.\n" {
			t.Errorf("expected switch to warn about the override, got %q, %v", stderr, err)
		}
	})

	t.Run("no servers", func(t *testing.T) {
		config.SetBaseDir(t.TempDir())
		t.Setenv("OPENRELIK_SERVER_URL", "")
		stdout, stderr, err := runAuth(t, "", false, "list")
		if err != nil || stdout != "" || !strings.Contains(stderr, "No servers are saved") {
			t.Errorf("got stdout %q, stderr %q, err %v", stdout, stderr, err)
		}
	})
}

func TestAuthServerFlag(t *testing.T) {
	server := newMeServer(t, "good-key")
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	t.Setenv("OPENRELIK_SERVER_URL", "")

	if _, _, err := runAuth(t, "good-key\n", false, "login", "--server", server.URL, "--api-key-stdin"); err != nil {
		t.Fatalf("login --server failed: %v", err)
	}
	if _, _, err := runAuth(t, "good-key\n", false, "login", "--server", "http://other.example", "--api-key-stdin", server.URL); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Errorf("expected an error for a SERVER that differs from --server, got %v", err)
	}
	stdout, _, err := runAuth(t, "", false, "logout", "--server", server.URL+"/")
	if err != nil || stdout != "Logged out of "+server.URL+"\n" {
		t.Errorf("logout --server: stdout %q, err %v", stdout, err)
	}
}
