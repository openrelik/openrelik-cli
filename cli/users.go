package cli

import (
	"github.com/openrelik/openrelik-cli/util"
	"github.com/spf13/cobra"
)

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user",
		Aliases: []string{"users"},
		Short:   "Manage users",
		Long:    `Manage OpenRelik user accounts.`,
	}

	cmd.AddCommand(newMeCmd())
	return cmd
}

func newMeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "me",
		Short: "Get current user profile",
		Long:  `Display the profile of the currently authenticated user.`,
		Example: `  # Show current user
  openrelik user me

  # Output as JSON
  openrelik user me --format json`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}

			user, _, err := client.Users().GetMe(cmd.Context())
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &UserMeView{User: user})
		},
	}
}
