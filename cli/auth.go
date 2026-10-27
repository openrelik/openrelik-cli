package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var passwordReader = func(fd int) ([]byte, error) {
	return term.ReadPassword(fd)
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
		Long:  `Manage OpenRelik authentication credentials.`,
	}

	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newLogoutCmd())
	cmd.AddCommand(newAuthSwitchCmd())
	cmd.AddCommand(newAuthStatusCmd())
	cmd.AddCommand(newAuthListCmd())
	return cmd
}

func newLoginCmd() *cobra.Command {
	var keyStdin bool

	cmd := &cobra.Command{
		Use:   "login [SERVER]",
		Short: "Login to OpenRelik",
		Long: `Log in to an OpenRelik server, and save the server URL and API key in
~/.openrelik/ for use by all subsequent commands. The server becomes the
active server.

SERVER is the server URL; you are asked for it if it is omitted. The API
key is read from the terminal without showing it. To log in without a
terminal, give SERVER and pipe the key in with --api-key-stdin.

The login is checked with the server before anything is saved. If it
fails, nothing is saved, so a typo can't replace a working login.

As an alternative to logging in, set the OPENRELIK_SERVER_URL and
OPENRELIK_API_KEY environment variables; they take precedence over the
saved login.`,
		Example: `  # Interactive login
  openrelik auth login

  # Log in to a given server
  openrelik auth login https://openrelik.example.org

  # Log in from a script, with the key in a file
  openrelik auth login https://openrelik.example.org --api-key-stdin < key.txt

  # Use environment variables instead of logging in
  export OPENRELIK_SERVER_URL=http://localhost:8710
  export OPENRELIK_API_KEY=your-refresh-token`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			args, err := serverArgs(args)
			if err != nil {
				return err
			}
			var server, key string
			in := bufio.NewReader(cmd.InOrStdin())

			if len(args) > 0 {
				server = args[0]
			} else {
				if keyStdin {
					return usageErrorf("--api-key-stdin needs SERVER, since the key takes up stdin")
				}
				fmt.Fprint(cmd.ErrOrStderr(), "OpenRelik Server URL (e.g., http://localhost:8710): ")
				line, _ := in.ReadString('\n')
				server = strings.TrimSpace(line)
			}
			if server == "" {
				return usageErrorf("server URL is required")
			}
			server, err = normalizeServerURL(server)
			if err != nil {
				return err
			}

			if keyStdin {
				line, err := in.ReadString('\n')
				if err != nil && !errors.Is(err, io.EOF) {
					return fmt.Errorf("error reading API key: %w", err)
				}
				key = strings.TrimSpace(line)
			} else {
				if !stdinIsTerminal(cmd.InOrStdin()) {
					return errors.New("no terminal to read the API key from; pipe it in with --api-key-stdin")
				}
				fmt.Fprint(cmd.ErrOrStderr(), "OpenRelik API Key (refresh token): ")
				byteKey, err := passwordReader(int(syscall.Stdin))
				fmt.Fprintln(cmd.ErrOrStderr()) // Print a newline after reading the password
				if err != nil {
					return fmt.Errorf("error reading API key: %w", err)
				}
				key = strings.TrimSpace(string(byteKey))
			}
			if key == "" {
				return fmt.Errorf("API key is required")
			}

			// Check the login before saving anything, so a typo in the URL
			// or key doesn't replace working credentials.
			client, err := openrelik.NewClient(server, key)
			if err != nil {
				return fmt.Errorf("could not log in to %s: %w\nNothing was saved.", server, err)
			}
			user, _, err := client.Users().GetMe(cmd.Context())
			if err != nil {
				return fmt.Errorf("could not log in to %s: %w\nNothing was saved.", server, err)
			}

			settings, err := loadSettings()
			if err != nil {
				return err
			}
			if settings.GetServerByURL(server) == nil {
				settings.Servers = append(settings.Servers, config.ServerConfig{URL: server})
			}
			settings.ActiveServer = server
			if err := config.SaveSettings(settings); err != nil {
				return fmt.Errorf("error saving settings: %w", err)
			}

			creds, err := loadCredentials()
			if err != nil {
				return err
			}
			creds.APIKeys[server] = key
			if err := config.SaveCredentials(creds); err != nil {
				return fmt.Errorf("error saving credentials: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s as %s\n", server, user.Username)
			warnServerOverride(cmd)
			return nil
		},
	}

	cmd.Flags().BoolVar(&keyStdin, "api-key-stdin", false, "Read the API key from stdin")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [SERVER]",
		Short: "Remove a saved server and its API key",
		Long: `Remove a saved server and its API key from ~/.openrelik/.

SERVER is a server URL or its number in 'auth list', and defaults to the
active server. If the active server is removed, no server is active
afterwards: choose another one with 'auth switch', or log in again.
Commands never fall back to another saved server on their own.

The API key is only removed from this computer. It stays valid on the
server until it is revoked there.`,
		Example: `  # Log out of the active server
  openrelik auth logout

  # Remove the second server in 'auth list'
  openrelik auth logout 2`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			args, err := serverArgs(args)
			if err != nil {
				return err
			}
			settings, err := loadSettings()
			if err != nil {
				return err
			}
			arg := settings.ActiveServer
			if len(args) > 0 {
				arg = args[0]
			} else if settings.GetActiveServer() == nil {
				return errors.New("no server is active; give SERVER to choose which one to log out of (see 'openrelik auth list')")
			}
			i, err := findServer(settings, arg)
			if err != nil {
				return err
			}
			server := settings.Servers[i].URL

			settings.Servers = append(settings.Servers[:i], settings.Servers[i+1:]...)
			wasActive := settings.ActiveServer == server
			if wasActive {
				settings.ActiveServer = ""
			}
			if err := config.SaveSettings(settings); err != nil {
				return fmt.Errorf("error saving settings: %w", err)
			}
			creds, err := loadCredentials()
			if err != nil {
				return err
			}
			delete(creds.APIKeys, server)
			if err := config.SaveCredentials(creds); err != nil {
				return fmt.Errorf("error saving credentials: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s\n", server)
			if wasActive && !quiet && len(settings.Servers) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No server is active now; choose one with 'openrelik auth switch'.")
			}
			return nil
		},
	}
}

func newAuthSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "switch [SERVER]",
		Short: "Switch active server",
		Long: `Make another saved server the active server, used by all subsequent
commands.

SERVER is a server URL or its number in 'auth list'. Without SERVER, the
saved servers are listed and you are asked to pick one; without a terminal
to ask on, the command fails instead. Use 'auth login' to add a server.`,
		Example: `  # Choose from the saved servers
  openrelik auth switch

  # Switch to a server by URL
  openrelik auth switch https://openrelik.example.org

  # Switch to the second server in 'auth list'
  openrelik auth switch 2`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			args, err := serverArgs(args)
			if err != nil {
				return err
			}
			settings, err := loadSettings()
			if err != nil {
				return err
			}
			if len(settings.Servers) == 0 {
				return errors.New("no servers are saved; add one with 'openrelik auth login'")
			}

			var arg string
			if len(args) > 0 {
				arg = args[0]
			} else {
				if !stdinIsTerminal(cmd.InOrStdin()) {
					return errors.New("give SERVER (a URL or a number from 'openrelik auth list'); there is no terminal to ask on")
				}
				w := cmd.ErrOrStderr()
				fmt.Fprintln(w, "Select a server:")
				for i, srv := range settings.Servers {
					active := ""
					if srv.URL == settings.ActiveServer {
						active = " [ACTIVE]"
					}
					fmt.Fprintf(w, "%d) %s%s\n", i+1, srv.URL, active)
				}
				fmt.Fprint(w, "Enter number: ")
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				arg = strings.TrimSpace(line)
				if _, err := strconv.Atoi(arg); err != nil {
					return usageErrorf("invalid input: %q is not a number", arg)
				}
			}
			i, err := findServer(settings, arg)
			if err != nil {
				return err
			}

			settings.ActiveServer = settings.Servers[i].URL
			if err := config.SaveSettings(settings); err != nil {
				return fmt.Errorf("failed to save settings: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Switched to %s\n", settings.ActiveServer)
			warnServerOverride(cmd)
			return nil
		},
	}
}

// normalizeServerURL checks that server is an http or https URL and drops a
// trailing slash, so the same server isn't saved twice. A path is allowed,
// for a server under a sub-path, but a user, query or fragment is not.
func normalizeServerURL(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || strings.ContainsAny(server, "?#") {
		return "", usageErrorf("invalid server URL %q: give a URL such as https://openrelik.example.org", server)
	}
	return strings.TrimRight(server, "/"), nil
}

// findServer returns the index of the saved server that arg names, either
// by URL or by its number in 'auth list'.
func findServer(settings *config.Settings, arg string) (int, error) {
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(settings.Servers) {
			return 0, usageErrorf("invalid choice %d: there are %d saved servers (see 'openrelik auth list')", n, len(settings.Servers))
		}
		return n - 1, nil
	}
	for i, srv := range settings.Servers {
		if strings.TrimRight(srv.URL, "/") == strings.TrimRight(arg, "/") {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no saved server %q (see 'openrelik auth list', or add it with 'openrelik auth login %s')", arg, arg)
}

// loadSettings loads the settings file, or empty settings if there is none.
func loadSettings() (*config.Settings, error) {
	settings, err := config.LoadSettings()
	if errors.Is(err, os.ErrNotExist) {
		return &config.Settings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error loading settings: %w", err)
	}
	return settings, nil
}

// loadCredentials loads the credentials file, or empty credentials if there
// is none.
func loadCredentials() (*config.Credentials, error) {
	creds, err := config.LoadCredentials()
	if errors.Is(err, os.ErrNotExist) {
		return &config.Credentials{APIKeys: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error loading credentials: %w", err)
	}
	return creds, nil
}

// serverArgs lets the global --server flag stand in for the SERVER argument
// of login, logout and switch, so it isn't silently ignored there.
func serverArgs(args []string) ([]string, error) {
	if serverURL == "" {
		return args, nil
	}
	if len(args) == 0 {
		return []string{serverURL}, nil
	}
	if strings.TrimRight(args[0], "/") != strings.TrimRight(serverURL, "/") {
		return nil, usageErrorf("SERVER %q and --server %q differ; give only one", args[0], serverURL)
	}
	return args, nil
}

// warnServerOverride tells the user on stderr when an environment variable
// or flag overrides the active server they just chose, so commands won't
// use it.
func warnServerOverride(cmd *cobra.Command) {
	c := resolveCredentials()
	if quiet || c.ServerURL == "" || c.ServerURL == c.ActiveServer {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Note: commands use %s instead while %s is set.\n", c.ServerURL, c.ServerSource)
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the server in use and check the login",
		Long: `Show the server and API key that commands use, where each comes from, and
the user the API key logs in as.

The server and API key are taken from the OPENRELIK_SERVER_URL and
OPENRELIK_API_KEY environment variables if they are set, and otherwise from
the active server chosen with 'auth login' or 'auth switch'. The API key
itself is never shown.

The login is checked with the server, and the command fails if no server or
API key is set, or if the server doesn't accept the key.`,
		Example: `  # Check which server commands use
  openrelik auth status

  # Check the login in a script
  openrelik auth status -q || openrelik auth login`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := resolveCredentials()
			view := &AuthStatusView{credentials: c}
			var checkErr error
			switch {
			case c.ServerURL == "":
				checkErr = &credentialsError{"no server is set; run 'openrelik auth login' or set OPENRELIK_SERVER_URL"}
			case c.APIKey == "":
				checkErr = &credentialsError{fmt.Sprintf("no API key is set for %s; run 'openrelik auth login' or set OPENRELIK_API_KEY", c.ServerURL)}
			default:
				var client *openrelik.Client
				client, checkErr = NewAPIClient()
				if checkErr == nil {
					view.User, _, checkErr = client.Users().GetMe(cmd.Context())
				}
				if checkErr != nil {
					checkErr = fmt.Errorf("the login to %s doesn't work: %w", c.ServerURL, checkErr)
				}
			}
			if checkErr != nil {
				view.Error = checkErr.Error()
			}
			if err := formatAndPrint(cmd, view); err != nil {
				return err
			}
			return checkErr
		},
	}
}

func newAuthListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all configured servers",
		Long: `List the servers saved with 'auth login', with the active one marked.

The numbers can be passed to 'auth switch' and 'auth logout'. If the
OPENRELIK_SERVER_URL environment variable is set, commands use that server
instead of the active one, and the list says so.`,
		Example: `  # List saved servers
  openrelik auth list

  # Print the active server's URL
  openrelik auth list --format json | jq -r '.[] | select(.active) | .url'`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := loadSettings()
			if err != nil {
				return err
			}
			creds, err := loadCredentials()
			if err != nil {
				return err
			}
			if len(settings.Servers) == 0 && !quiet && outputFormat == "human" {
				fmt.Fprintln(cmd.ErrOrStderr(), "No servers are saved; add one with 'openrelik auth login'.")
			}
			view := &AuthListView{credentials: resolveCredentials()}
			for i, srv := range settings.Servers {
				view.Servers = append(view.Servers, savedServer{
					Number:      i + 1,
					URL:         srv.URL,
					Active:      srv.URL == settings.ActiveServer,
					APIKeySaved: creds.APIKeys[srv.URL] != "",
				})
			}
			return formatAndPrint(cmd, view)
		},
	}
}
