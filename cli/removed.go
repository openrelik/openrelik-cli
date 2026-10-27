package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// removedCmd is a hidden stand-in for a command that was replaced. Running it
// fails with the command to use instead, so old scripts get a clear error
// rather than "unknown command". note adds anything that behaves differently.
func removedCmd(use, replacement, note string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Aliases: aliases,
		Hidden:  true,
		// Accept the old command's flags, such as --chunk-size.
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := fmt.Sprintf("'%s' was removed. Use instead:\n\n  %s", cmd.CommandPath(), replacement)
			if note != "" {
				msg += "\n\n" + note
			}
			return errors.New(msg)
		},
	}
}

// removedFileCmds are the 'file' subcommands replaced by 'folder list' and
// the top-level 'upload' and 'download'.
func removedFileCmds() []*cobra.Command {
	return []*cobra.Command{
		removedCmd("list", "openrelik folder list <FOLDER_ID>",
			`With --format json, files are now under "files": jq '.files[]'.`, "ls"),
		removedCmd("upload", "openrelik upload <FILE_PATH>... --folder <FOLDER_ID>", ""),
		removedCmd("download", "openrelik download --file <ID> [DESTINATION]", ""),
	}
}

// removedFolderCmds are the 'folder' subcommands replaced by 'upload'.
func removedFolderCmds() []*cobra.Command {
	return []*cobra.Command{
		removedCmd("mirror", "openrelik upload <LOCAL_FOLDER> [--folder <FOLDER_ID>]",
			"With --folder, LOCAL_FOLDER is created as a subfolder of FOLDER_ID instead of\nits contents being uploaded directly into FOLDER_ID."),
	}
}
