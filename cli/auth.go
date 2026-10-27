package cli

import (
	"bufio"
	"errors"
	"fmt"
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
	cmd.AddCommand(newAuthSwitchCmd())
	cmd.AddCommand(newAuthStatusCmd())
	cmd.AddCommand(newAuthListCmd())
	return cmd
}

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Login to OpenRelik",
		Long: `Interactively prompt for a server URL and API key, check that the server
accepts the key, then save them to ~/.openrelik/ for use by all subsequent
commands. If the login fails, nothing is saved.

As an alternative to interactive login, set the OPENRELIK_SERVER_URL and
OPENRELIK_API_KEY environment variables — they take precedence over the
stored credentials.`,
		Example: `  # Interactive login
  openrelik auth login

  # Non-interactive via environment variables
  export OPENRELIK_SERVER_URL=http://localhost:8710
  export OPENRELIK_API_KEY=your-refresh-token`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			var server, key string

			fmt.Fprint(cmd.OutOrStdout(), "OpenRelik Server URL (e.g., http://localhost:8710): ")
			scanner := bufio.NewScanner(cmd.InOrStdin())
			if scanner.Scan() {
				server = strings.TrimSpace(scanner.Text())
			}
			if server == "" {
				return fmt.Errorf("server URL is required")
			}

			fmt.Fprint(cmd.OutOrStdout(), "OpenRelik API Key (refresh token): ")
			byteKey, err := passwordReader(int(syscall.Stdin))
			fmt.Fprintln(cmd.OutOrStdout()) // Print a newline after reading the password
			if err != nil {
				return fmt.Errorf("error reading API key: %w", err)
			}
			key = string(byteKey)

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

			settings, err := config.LoadSettings()
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("error loading settings: %w", err)
				}
				// If settings don't exist, create the first one
				settings = &config.Settings{
					ActiveServer: server,
					Servers:      []config.ServerConfig{{URL: server}},
				}
			} else {
				// Check if server already exists
				existing := settings.GetServerByURL(server)
				if existing != nil {
					// Server exists, just make it active
					settings.ActiveServer = existing.URL
				} else {
					// Server does not exist, add it and make it active
					settings.Servers = append(settings.Servers, config.ServerConfig{URL: server})
					settings.ActiveServer = server
				}
			}

			err = config.SaveSettings(settings)
			if err != nil {
				return fmt.Errorf("error saving settings: %w", err)
			}

			creds, err := config.LoadCredentials()
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("error loading credentials: %w", err)
				}
				creds = &config.Credentials{APIKeys: make(map[string]string)}
			}
			if creds.APIKeys == nil {
				creds.APIKeys = make(map[string]string)
			}
			creds.APIKeys[server] = key

			err = config.SaveCredentials(creds)
			if err != nil {
				return fmt.Errorf("error saving credentials: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s as %s\n", server, user.Username)
			return nil
		},
	}
}

func newAuthSwitchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "switch",
		Short: "Switch active server",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return fmt.Errorf("failed to load settings: %w", err)
			}
			if len(settings.Servers) == 0 {
				return fmt.Errorf("no servers configured")
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Select a server:")
			for i, srv := range settings.Servers {
				active := ""
				if srv.URL == settings.ActiveServer {
					active = " [ACTIVE]"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d) %s%s\n", i+1, srv.URL, active)
			}

			fmt.Fprint(cmd.OutOrStdout(), "Enter number: ")
			scanner := bufio.NewScanner(cmd.InOrStdin())
			var choice int
			if scanner.Scan() {
				choiceStr := strings.TrimSpace(scanner.Text())
				var err error
				choice, err = strconv.Atoi(choiceStr)
				if err != nil {
					return fmt.Errorf("invalid input: %q is not a number", choiceStr)
				}
			}

			if choice < 1 || choice > len(settings.Servers) {
				return fmt.Errorf("invalid choice")
			}

			settings.ActiveServer = settings.Servers[choice-1].URL
			err = config.SaveSettings(settings)
			if err != nil {
				return fmt.Errorf("failed to save settings: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Switched to %s\n", settings.ActiveServer)
			return nil
		},
	}
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
				checkErr = errors.New("no server is set; run 'openrelik auth login' or set OPENRELIK_SERVER_URL")
			case c.APIKey == "":
				checkErr = fmt.Errorf("no API key is set for %s; run 'openrelik auth login' or set OPENRELIK_API_KEY", c.ServerURL)
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
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := config.LoadSettings()
			if err != nil {
				return fmt.Errorf("failed to load settings: %w", err)
			}
			if len(settings.Servers) == 0 {
				return fmt.Errorf("no servers configured")
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Configured servers:")
			for _, srv := range settings.Servers {
				active := ""
				if srv.URL == settings.ActiveServer {
					active = " [ACTIVE]"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "- %s%s\n", srv.URL, active)
			}
			return nil
		},
	}
}
