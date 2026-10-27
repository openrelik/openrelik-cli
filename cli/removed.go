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
	// 'openrelik help <command>' doesn't run RunE, so the help text carries
	// the same notice.
	long := "This command was removed. Use instead:\n\n  " + replacement
	if note != "" {
		long += "\n\n" + note
	}
	return &cobra.Command{
		Use:     use,
		Aliases: aliases,
		Short:   "Removed; use '" + replacement + "'",
		Long:    long,
		Hidden:  true,
		// Accept the old command's flags, such as --chunk-size.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := fmt.Sprintf("'%s' was removed. Use instead:\n\n  %s", cmd.CommandPath(), replacement)
			if note != "" {
				msg += "\n\n" + note
			}
			return errors.New(msg)
		},
	}
}

// removedFlag adds a hidden stand-in for a flag of cmd that was replaced.
// Using it fails with the command to use instead, so old scripts get a clear
// error rather than "unknown flag". The check runs before cmd's positional
// args are validated, since the old usage often has too few of them.
func removedFlag(cmd *cobra.Command, name, replacement string) {
	cmd.Flags().String(name, "", "")
	cmd.Flags().MarkHidden(name)
	validate := cmd.Args
	cmd.Args = func(c *cobra.Command, args []string) error {
		if c.Flags().Changed(name) {
			return fmt.Errorf("'%s --%s' was removed. Use instead:\n\n  %s", c.CommandPath(), name, replacement)
		}
		if validate == nil {
			return nil
		}
		return validate(c, args)
	}
}

// removedFileCmds are the 'file' subcommands replaced by 'folder list' and
// the top-level 'upload' and 'download'.
func removedFileCmds() []*cobra.Command {
	return []*cobra.Command{
		removedCmd("list", "openrelik folder list <FOLDER_ID>",
			`With --format json, files are now under "files": jq '.files[]'.`, "ls"),
		removedCmd("upload", "openrelik upload <FILE_PATH>... <FOLDER>", ""),
		removedCmd("download", "openrelik download <FILE> [DESTINATION]", ""),
	}
}

// removedFolderCmds are the 'folder' subcommands replaced by 'upload'.
func removedFolderCmds() []*cobra.Command {
	return []*cobra.Command{
		removedCmd("mirror", "openrelik upload <LOCAL_FOLDER> [FOLDER]",
			"With FOLDER, LOCAL_FOLDER is created as a subfolder of FOLDER instead of\nits contents being uploaded directly into FOLDER."),
	}
}
