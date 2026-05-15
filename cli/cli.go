package cli

import (
	"fmt"
	"os"

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
		Long:             `A command line tool to interact with the OpenRelik API`,
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

	return cmd
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

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
