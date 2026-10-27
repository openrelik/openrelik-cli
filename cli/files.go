package cli

import (
	"strconv"

	"github.com/openrelik/openrelik-cli/util"
	"github.com/spf13/cobra"
)

func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "file",
		Aliases: []string{"files"},
		Short:   "Manage files",
		Long:    `Inspect files stored in OpenRelik.`,
	}

	cmd.AddCommand(newFileInfoCmd())
	cmd.AddCommand(removedFileCmds()...)
	return cmd
}

func newFileInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <ID>",
		Short: "Get file metadata",
		Long: `Display detailed metadata for a file, including size, hashes, MIME type,
and timestamps.

ID is the integer file ID, as shown by 'folder list <FOLDER_ID>'.`,
		Example: `  # Show metadata for file 123
  openrelik file info 123

  # Output as JSON
  openrelik file info 123 --format json`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			fileID, err := strconv.Atoi(args[0])
			if err != nil {
				return err
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			file, _, err := client.Files().Info(cmd.Context(), fileID)
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &FileInfoView{File: file})
		},
	}
}
