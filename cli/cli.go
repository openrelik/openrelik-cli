package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

var (
	serverURL    string
	outputFormat string
	quiet        bool
)

// outputFormats are the values --format accepts.
var outputFormats = []string{"human", "json", "verbose"}

// formatValue is the --format flag. It rejects unknown formats while the
// flags are parsed, so a typo fails before any request is made.
type formatValue struct{ p *string }

func (f formatValue) String() string { return *f.p }
func (f formatValue) Type() string   { return "string" }
func (f formatValue) Set(s string) error {
	if !slices.Contains(outputFormats, s) {
		return fmt.Errorf("must be one of %s", strings.Join(outputFormats, ", "))
	}
	*f.p = s
	return nil
}

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:              "openrelik",
		Short:            "OpenRelik CLI client",
		Long:             `A command line tool to interact with OpenRelik`,
		Version:          Version,
		TraverseChildren: true,
		SilenceErrors:    true,
		SilenceUsage:     true,
	}

	cmd.CompletionOptions.DisableDefaultCmd = true

	serverURL, outputFormat = "", "human"
	cmd.PersistentFlags().StringVar(&serverURL, "server", "", "Server URL to use instead of the active one (overrides OPENRELIK_SERVER_URL)")
	cmd.PersistentFlags().Var(formatValue{&outputFormat}, "format", "Output format ("+strings.Join(outputFormats, ", ")+")")
	cmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress all output")

	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newUserCmd())
	cmd.AddCommand(newFolderCmd())
	cmd.AddCommand(newFileCmd())
	cmd.AddCommand(newWorkerCmd())
	cmd.AddCommand(newTemplateCmd())
	cmd.AddCommand(newWorkflowCmd())
	cmd.AddCommand(newUploadCmd())
	cmd.AddCommand(newDownloadCmd())
	cmd.AddCommand(newRunCmd())
	cmd.AddCommand(newSkillCmd())

	requireKnownSubcommands(cmd)
	return cmd
}

// requireKnownSubcommands makes every command group in the tree (commands
// without their own action, including the root) show help when run without
// arguments and fail on an unknown subcommand. Without this, Cobra prints
// help and exits 0 for 'openrelik file bogus' because TraverseChildren skips
// its unknown-command check.
func requireKnownSubcommands(cmd *cobra.Command) {
	if !cmd.Runnable() {
		cmd.Args = unknownSubcommandArgs
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
	}
	for _, c := range cmd.Commands() {
		requireKnownSubcommands(c)
	}
}

// unknownSubcommandArgs rejects any positional argument to a command group,
// suggesting close matches like Cobra does for the root command.
func unknownSubcommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if cmd.SuggestionsMinimumDistance <= 0 {
		// Cobra only applies its default in its own unknown-command check.
		cmd.SuggestionsMinimumDistance = 2
	}
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		msg += "\n\nDid you mean this?\n\t" + strings.Join(suggestions, "\n\t")
	}
	return fmt.Errorf("%s\n\nRun '%s --help' for usage.", msg, cmd.CommandPath())
}

// credentials are the server and API key that commands use, and where each
// came from, for 'auth status'.
type credentials struct {
	ServerURL    string
	ServerSource string // e.g. "OPENRELIK_SERVER_URL"; empty if no server is set
	APIKey       string
	APIKeySource string // empty if no key is set
	// ActiveServer is the active server in the settings file, which an
	// environment variable or flag may override.
	ActiveServer string
}

// resolveCredentials finds the server in order of precedence: the --server
// flag, OPENRELIK_SERVER_URL, then the active server in the config files.
// The API key comes from OPENRELIK_API_KEY or, failing that, the key saved
// for the server, whichever way the server was chosen. There is no flag for
// the key, since command lines show up in process lists and shell history.
func resolveCredentials() credentials {
	var c credentials
	if settings, _ := config.LoadSettings(); settings != nil {
		if active := settings.GetActiveServer(); active != nil {
			c.ActiveServer = active.URL
		}
	}
	switch {
	case serverURL != "":
		c.ServerURL, c.ServerSource = strings.TrimRight(serverURL, "/"), "--server"
	case os.Getenv("OPENRELIK_SERVER_URL") != "":
		c.ServerURL, c.ServerSource = strings.TrimRight(os.Getenv("OPENRELIK_SERVER_URL"), "/"), "OPENRELIK_SERVER_URL"
	case c.ActiveServer != "":
		c.ServerURL = c.ActiveServer
		c.ServerSource, _ = config.SettingsPath()
	}

	switch {
	case os.Getenv("OPENRELIK_API_KEY") != "":
		c.APIKey, c.APIKeySource = os.Getenv("OPENRELIK_API_KEY"), "OPENRELIK_API_KEY"
	case c.ServerURL != "":
		if creds, _ := config.LoadCredentials(); creds != nil && creds.APIKeys[c.ServerURL] != "" {
			c.APIKey = creds.APIKeys[c.ServerURL]
			c.APIKeySource, _ = config.CredentialsPath()
		}
	}
	return c
}

// NewAPIClient can be replaced to customize how the API client is constructed:
//
//	cli.NewAPIClient = func() (*openrelik.Client, error) {
//		return openrelik.NewClient(myURL, myKey, openrelik.WithHTTPClient(myHTTPClient))
//	}
var NewAPIClient = func() (*openrelik.Client, error) {
	c := resolveCredentials()
	if c.ServerURL == "" {
		return nil, fmt.Errorf("server URL is required (use --server or the OPENRELIK_SERVER_URL env var, or run 'openrelik auth login')")
	}
	if c.APIKey == "" {
		return nil, fmt.Errorf("API key is required (use OPENRELIK_API_KEY env var, or run 'openrelik auth login')")
	}
	return openrelik.NewClient(c.ServerURL, c.APIKey)
}

func newClient() (*openrelik.Client, error) { return NewAPIClient() }

// formatAndPrint outputs the result in the requested format.
func formatAndPrint(cmd *cobra.Command, result interface{}) error {
	if quiet {
		return nil
	}
	switch outputFormat {
	case "json":
		payload := result
		if u, ok := result.(util.JSONUnwrapper); ok {
			payload = u.UnwrapJSON()
		}
		return util.FprintJSON(cmd.OutOrStdout(), payload)
	case "verbose":
		if vr, ok := result.(util.VerboseRenderer); ok {
			return vr.RenderVerbose(cmd.OutOrStdout())
		}
		payload := result
		if u, ok := result.(util.JSONUnwrapper); ok {
			payload = u.UnwrapJSON()
		}
		util.FprintStruct(cmd.OutOrStdout(), payload)
		return nil
	default: // "human"
		if hr, ok := result.(util.HumanRenderer); ok {
			return hr.RenderHuman(cmd.OutOrStdout())
		}
		util.FprintStruct(cmd.OutOrStdout(), result)
		return nil
	}
}

// printTruncationHint tells the user on stderr that a list was truncated.
// It is only shown for human output so piped and JSON output stay clean.
// advice tells the user how to see the rest, e.g. "Omit --limit to see all."
func printTruncationHint(cmd *cobra.Command, shown, total int, noun, advice string) {
	if quiet || outputFormat != "human" || shown >= total {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "\nShowing %d of %d %s. %s\n", shown, total, noun, advice)
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
