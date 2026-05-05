package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-cli/view"
	"github.com/spf13/cobra"
)

var (
	parentID int
	maxDepth int
)

func newFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "folder",
		Short: "Manage folders",
		Long:  `Create, list, and mirror folders in OpenRelik.`,
	}

	cmd.AddCommand(newListFoldersCmd())
	cmd.AddCommand(newCreateFolderCmd())
	cmd.AddCommand(newMirrorFolderCmd())
	return cmd
}

func newListFoldersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [PARENT_ID]",
		Short: "List folders",
		Long: `List folders in OpenRelik.

Without PARENT_ID, lists all root folders. With PARENT_ID, lists the
direct subfolders of that folder. PARENT_ID is the integer ID shown
by a previous 'folder list' call.`,
		Example: `  # List root folders
  openrelik folder list

  # List subfolders of folder 42
  openrelik folder list 42

  # Output as JSON
  openrelik folder list --format json`,
		Args:  util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			var pID int
			var err error
			if len(args) > 0 {
				pID, err = strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid parent ID: %w", err)
				}
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			var folders []openrelik.Folder
			if pID != 0 {
				folders, _, err = client.Folders().ListSubFolders(cmd.Context(), pID)
			} else {
				folders, _, err = client.Folders().ListRootFolders(cmd.Context())
			}

			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &view.FolderListView{Folders: folders})
		},
	}
}

func newCreateFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <NAME>",
		Short: "Create a folder",
		Long: `Create a new folder in OpenRelik.

Without --parent, creates a root-level folder. With --parent, creates a
subfolder inside the specified parent folder.`,
		Example: `  # Create a root folder
  openrelik folder create "Investigations"

  # Create a subfolder inside folder 42
  openrelik folder create "Case 2024-001" --parent 42`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}

			name := args[0]
			var folder *openrelik.Folder
			if parentID != 0 {
				folder, _, err = client.Folders().CreateSubFolder(cmd.Context(), parentID, name)
			} else {
				folder, _, err = client.Folders().CreateRootFolder(cmd.Context(), name)
			}

			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &view.FolderCreatedView{Folder: folder})
		},
	}

	cmd.Flags().IntVarP(&parentID, "parent", "p", 0, "Parent folder ID")
	return cmd
}

func newMirrorFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror <LOCAL_FOLDER> [FOLDER_ID]",
		Short: "Mirror a local folder tree into OpenRelik",
		Long: `Recursively upload a local directory tree into OpenRelik, preserving the
folder hierarchy up to --depth levels deep.

LOCAL_FOLDER is the path to the local directory to mirror. FOLDER_ID is the
integer ID of an existing OpenRelik folder to mirror into; if omitted, a new
root folder named after the local directory is created automatically.`,
		Example: `  # Mirror into a new root folder (named after the local directory)
  openrelik folder mirror ./evidence/

  # Mirror into an existing folder
  openrelik folder mirror ./evidence/ 42

  # Mirror up to 5 levels deep with a larger chunk size
  openrelik folder mirror ./evidence/ 42 --depth 5 --chunk-size 8388608`,
		Args:         util.UseArgs(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			localPath := args[0]

			client, err := newClient()
			if err != nil {
				return err
			}

			var rootFolderID int
			if len(args) == 2 {
				rootFolderID, err = strconv.Atoi(args[1])
				if err != nil {
					return fmt.Errorf("invalid folder ID: %w", err)
				}
			} else {
				folder, _, err := client.Folders().CreateRootFolder(cmd.Context(), filepath.Base(localPath))
				if err != nil {
					return fmt.Errorf("failed to create root folder: %w", err)
				}
				rootFolderID = folder.ID
			}

			return mirrorDir(cmd.Context(), cmd.ErrOrStderr(), client, localPath, rootFolderID, 0)
		},
	}

	cmd.Flags().IntVarP(&maxDepth, "depth", "d", 3, "Maximum subfolder depth to mirror")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 4*1024*1024, "Chunk size in bytes for uploads")
	return cmd
}

func mirrorDir(ctx context.Context, out io.Writer, client *openrelik.Client, localPath string, remoteFolderID int, depth int) error {
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", localPath, err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(localPath, entry.Name())
		if entry.IsDir() {
			if depth >= maxDepth {
				continue
			}
			folder, _, err := client.Folders().CreateSubFolder(ctx, remoteFolderID, entry.Name())
			if err != nil {
				return fmt.Errorf("failed to create subfolder %s: %w", entry.Name(), err)
			}
			if err := mirrorDir(ctx, out, client, entryPath, folder.ID, depth+1); err != nil {
				return err
			}
		} else {
			if _, err := uploadFileWithProgress(ctx, out, client, entryPath, remoteFolderID); err != nil {
				return err
			}
		}
	}
	return nil
}
