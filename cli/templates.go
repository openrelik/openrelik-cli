package cli

import (
	"github.com/openrelik/openrelik-cli/util"
	"github.com/spf13/cobra"
)

func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "template",
		Aliases: []string{"templates"},
		Short:   "Manage workflow templates",
		Long:    `Inspect workflow templates available in OpenRelik.`,
	}

	cmd.AddCommand(newTemplateListCmd())
	return cmd
}

func newTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available workflow templates",
		Long:  `List all workflow templates available in the system.`,
		Example: `  # List all templates
  openrelik template list

  # Output as JSON
  openrelik template list --format json`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}

			templates, _, err := client.Templates().List(cmd.Context())
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &TemplateListView{Templates: templates})
		},
	}
}
