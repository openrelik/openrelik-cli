package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

var (
	serverURL    string
	apiKey       string
	outputFormat string
	quiet        bool
)

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

	cmd.PersistentFlags().StringVar(&outputFormat, "format", "human", "Output format (human, json, verbose)")
	cmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress all output")

	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newUserCmd())
	cmd.AddCommand(newFolderCmd())
	cmd.AddCommand(newFileCmd())
	cmd.AddCommand(newWorkerCmd())
	cmd.AddCommand(newTemplateCmd())
	cmd.AddCommand(newWorkflowCmd())
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

// NewAPIClient can be replaced to customize how the API client is constructed:
//
//	cli.NewAPIClient = func() (*openrelik.Client, error) {
//		return openrelik.NewClient(myURL, myKey, openrelik.WithHTTPClient(myHTTPClient))
//	}
var NewAPIClient = func() (*openrelik.Client, error) {
	s := serverURL
	if s == "" {
		s = os.Getenv("OPENRELIK_SERVER_URL")
	}
	var settings *config.Settings
	if s == "" {
		if settings, _ = config.LoadSettings(); settings != nil {
			if active := settings.GetActiveServer(); active != nil {
				s = active.URL
			}
		}
	}

	k := apiKey
	if k == "" {
		k = os.Getenv("OPENRELIK_API_KEY")
	}
	if k == "" {
		if creds, _ := config.LoadCredentials(); creds != nil && s != "" {
			if val, ok := creds.APIKeys[s]; ok {
				k = val
			}
		}
	}

	if s == "" {
		return nil, fmt.Errorf("server URL is required (use OPENRELIK_SERVER_URL env var, or run 'openrelik auth login')")
	}
	if k == "" {
		return nil, fmt.Errorf("API key is required (use OPENRELIK_API_KEY env var, or run 'openrelik auth login')")
	}

	return openrelik.NewClient(s, k)
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
func printTruncationHint(cmd *cobra.Command, shown, total int, noun string) {
	if quiet || outputFormat != "human" || shown >= total {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "\nShowing %d of %d %s. Omit --limit to see all.\n", shown, total, noun)
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
