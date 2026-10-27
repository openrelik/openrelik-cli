package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewClient(t *testing.T) {
	// Setup temp config dir
	tmpDir := t.TempDir()
	config.SetBaseDir(tmpDir)
	defer config.SetBaseDir("")

	// Helper to reset globals
	resetGlobals := func() {
		serverURL = ""
		os.Unsetenv("OPENRELIK_SERVER_URL")
		os.Unsetenv("OPENRELIK_API_KEY")
	}

	t.Run("ServerFlag", func(t *testing.T) {
		resetGlobals()
		defer resetGlobals()
		serverURL = "http://flag-server/"
		os.Setenv("OPENRELIK_SERVER_URL", "http://env-server")
		config.SaveCredentials(&config.Credentials{APIKeys: map[string]string{"http://flag-server": "saved-key"}})
		defer config.SaveCredentials(&config.Credentials{})

		c := resolveCredentials()
		if c.ServerURL != "http://flag-server" || c.ServerSource != "--server" || c.APIKey != "saved-key" {
			t.Errorf("resolveCredentials() = %+v, want the flag's server with its saved key", c)
		}

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

		config.SaveSettings(&config.Settings{
			ActiveServer: "http://config-server",
			Servers:      []config.ServerConfig{{URL: "http://config-server"}},
		})
		config.SaveCredentials(&config.Credentials{APIKeys: map[string]string{"http://config-server": "config-key"}})

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
		os.Setenv("OPENRELIK_API_KEY", "some-key")
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

func TestUnknownSubcommand(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")

	// 'run' asks the server for workers it doesn't know; fail that quickly.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("OPENRELIK_API_KEY", "test-key")
	t.Setenv("OPENRELIK_SERVER_URL", server.URL)

	tests := []struct {
		name       string
		args       []string
		wantErr    []string
		wantOutput string
	}{
		{name: "unknown root command", args: []string{"bogus"}, wantErr: []string{`unknown command "bogus" for "openrelik"`}},
		{name: "unknown group subcommand", args: []string{"file", "bogus", "42"}, wantErr: []string{`unknown command "bogus" for "openrelik file"`, "Run 'openrelik file --help' for usage."}},
		{name: "removed file list", args: []string{"file", "ls", "42", "--format", "json"}, wantErr: []string{"'openrelik file list' was removed", "openrelik folder list <FOLDER_ID>", ".files[]"}},
		{name: "removed file upload", args: []string{"files", "upload", "a.txt", "42", "--chunk-size", "8"}, wantErr: []string{"'openrelik file upload' was removed", "openrelik upload <FILE_PATH>... <FOLDER>"}},
		{name: "removed file download", args: []string{"file", "download", "123"}, wantErr: []string{"'openrelik file download' was removed", "openrelik download <FILE> [DESTINATION]"}},
		{name: "removed folder mirror", args: []string{"folder", "mirror", "./evidence", "42"}, wantErr: []string{"'openrelik folder mirror' was removed", "openrelik upload <LOCAL_FOLDER> [FOLDER]", "created as a subfolder of FOLDER"}},
		{name: "suggests close match", args: []string{"folder", "lst"}, wantErr: []string{"Did you mean this?", "list"}},
		{name: "run without workers", args: []string{"run", "strings"}, wantErr: []string{`unknown worker "strings", and the list of workers could not be updated`}},
		{name: "bare group shows help", args: []string{"file"}, wantOutput: "Usage:"},
		{name: "help for removed command shows the notice", args: []string{"help", "folder", "mirror"}, wantOutput: "This command was removed. Use instead:\n\n  openrelik upload <LOCAL_FOLDER>"},
		{name: "bare root shows help", args: []string{}, wantOutput: "Usage:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd()
			out := new(bytes.Buffer)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(tt.args)

			err := root.Execute()
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("expected error to contain %q, got %q", want, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() failed: %v", err)
			}
			if !strings.Contains(out.String(), tt.wantOutput) {
				t.Errorf("expected output to contain %q, got %q", tt.wantOutput, out.String())
			}
		})
	}
}

func TestFormatFlag(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")
	t.Setenv("OPENRELIK_SERVER_URL", "http://127.0.0.1:1")
	t.Setenv("OPENRELIK_API_KEY", "key")

	for _, args := range [][]string{
		{"--format", "yaml", "auth", "status"},
		{"auth", "status", "--format=JSON"},
	} {
		root := NewRootCmd()
		root.SetOut(new(bytes.Buffer))
		root.SetErr(new(bytes.Buffer))
		root.SetArgs(args)
		err := root.Execute()
		// The error must come from the flag, not from contacting the server.
		if err == nil || !strings.Contains(err.Error(), "must be one of human, json, verbose") {
			t.Errorf("%v: expected an invalid --format error, got %v", args, err)
		}
	}

	root := NewRootCmd()
	root.SetArgs([]string{"--format", "json", "auth", "list"})
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	if err := root.Execute(); err != nil || outputFormat != "json" {
		t.Errorf("--format json: format %q, err %v", outputFormat, err)
	}
	// Each new root command starts from the default again.
	NewRootCmd()
	if outputFormat != "human" {
		t.Errorf("outputFormat = %q after NewRootCmd, want human", outputFormat)
	}
}

// TestShortFlagsHaveOneMeaning checks that a short flag means the same long
// flag everywhere in the command tree, so -f is always --force.
func TestShortFlagsHaveOneMeaning(t *testing.T) {
	config.SetBaseDir(t.TempDir())
	defer config.SetBaseDir("")

	seen := map[string]string{} // shorthand -> "long (first command)"
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Shorthand == "" {
				return
			}
			if prev, ok := seen[f.Shorthand]; ok {
				if long, _, _ := strings.Cut(prev, " "); long != f.Name {
					t.Errorf("-%s is --%s in %q but %s", f.Shorthand, f.Name, cmd.CommandPath(), prev)
				}
				return
			}
			seen[f.Shorthand] = fmt.Sprintf("%s (in %q)", f.Name, cmd.CommandPath())
		})
		for _, c := range cmd.Commands() {
			walk(c)
		}
	}
	walk(NewRootCmd())

	for path, short := range map[string]string{"upload": "f", "download": "f"} {
		root := NewRootCmd()
		cmd, _, err := root.Find([]string{path})
		if err != nil || cmd.Flags().ShorthandLookup(short) == nil {
			t.Errorf("expected -%s on %s", short, path)
		}
	}
}
