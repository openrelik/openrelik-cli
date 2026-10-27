package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

var (
	parentID int
	maxDepth int
)

func newFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "folder",
		Aliases: []string{"folders"},
		Short:   "Manage folders",
		Long:    `Create, list, and upload to folders in OpenRelik.`,
	}

	cmd.AddCommand(newListFoldersCmd())
	cmd.AddCommand(newCreateFolderCmd())
	cmd.AddCommand(newUploadCmd())
	return cmd
}

// folderPageSize is the number of folders requested per API call.
const folderPageSize = 100

func newListFoldersCmd() *cobra.Command {
	var limit int
	var sortBy string
	var reverse bool

	cmd := &cobra.Command{
		Use:     "list [FOLDER_ID]",
		Aliases: []string{"ls"},
		Short:   "List folder contents",
		Long: `List folder contents in OpenRelik.

Without FOLDER_ID, lists all root folders. With FOLDER_ID, lists the
subfolders and files in that folder. FOLDER_ID is the integer ID shown
by a previous 'folder list' call.

Folder names end in '/' and have TYPE folder. Folder and file IDs are
separate, so pass folder IDs to 'folder list' and file IDs to commands
such as 'file info'.

Like 'ls -latr', entries are sorted by last update with the most
recently updated entry last. Use --sort name to sort by name and
-r to reverse the sort order. Use --limit N to show only the N most
recently updated entries, in the selected sort order.`,
		Example: `  # List all root folders, most recently updated last
  openrelik folder list

  # List subfolders and files in folder 42
  openrelik folder list 42

  # Most recently updated first
  openrelik folder list 42 -r

  # Sort by name
  openrelik folder list 42 --sort name

  # Show the 10 most recently updated entries
  openrelik folder list 42 --limit 10

  # Print the IDs of the files in folder 42
  openrelik folder list 42 --format json | jq '.files[].id'`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return fmt.Errorf("invalid limit: %d (must be 0 or greater)", limit)
			}
			if sortBy != "time" && sortBy != "name" {
				return fmt.Errorf("invalid sort: %q (must be time or name)", sortBy)
			}

			var fID int
			var err error
			if len(args) > 0 {
				fID, err = strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid folder ID: %w", err)
				}
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			var folders []openrelik.Folder
			var files []openrelik.FolderFile
			noun := "folders"
			if fID != 0 {
				folders, files, err = fetchFolderContents(cmd.Context(), client, fID)
				noun = "items"
			} else {
				folders, err = fetchAllRootFolders(cmd.Context(), client)
			}

			if err != nil {
				return err
			}

			entries := make([]listEntry, 0, len(folders)+len(files))
			for i := range folders {
				entries = append(entries, listEntry{folder: &folders[i]})
			}
			for i := range files {
				entries = append(entries, listEntry{file: &files[i]})
			}

			total := len(entries)
			entries = limitAndSortEntries(entries, limit, sortBy, reverse)

			if err := formatAndPrint(cmd, &FolderContentsView{Entries: entries}); err != nil {
				return err
			}
			printTruncationHint(cmd, len(entries), total, noun)
			return nil
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort", "time", "Sort by: time (oldest first) or name")
	cmd.Flags().BoolVarP(&reverse, "reverse", "r", false, "Reverse the sort order")
	cmd.Flags().IntVar(&limit, "limit", 0, "Show only the N most recently updated entries (0 shows all)")
	return cmd
}

// fetchFolderContents fetches the subfolders and files of a folder
// concurrently.
func fetchFolderContents(ctx context.Context, client *openrelik.Client, folderID int) ([]openrelik.Folder, []openrelik.FolderFile, error) {
	var (
		wg                 sync.WaitGroup
		folders            []openrelik.Folder
		files              []openrelik.FolderFile
		folderErr, fileErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		folders, _, folderErr = client.Folders().ListSubFolders(ctx, folderID)
	}()
	go func() {
		defer wg.Done()
		files, _, fileErr = client.Folders().ListFiles(ctx, folderID)
	}()
	wg.Wait()

	// Check the subfolder error first so a missing folder reports consistently.
	if folderErr != nil {
		return nil, nil, folderErr
	}
	if fileErr != nil {
		return nil, nil, fileErr
	}
	return folders, files, nil
}

// fetchAllRootFolders pages through the root folders endpoint and returns
// every root folder.
func fetchAllRootFolders(ctx context.Context, client *openrelik.Client) ([]openrelik.Folder, error) {
	var folders []openrelik.Folder
	for page := 1; ; page++ {
		result, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.ListOptions{Page: page, PageSize: folderPageSize})
		if err != nil {
			return nil, err
		}
		folders = append(folders, result.Folders...)
		if len(result.Folders) == 0 || len(folders) >= result.TotalCount {
			return folders, nil
		}
	}
}

// listEntry is a single row in a folder listing: either a folder or a file.
type listEntry struct {
	folder *openrelik.Folder
	file   *openrelik.FolderFile
}

func (e listEntry) updated() time.Time {
	if e.folder != nil {
		return e.folder.UpdatedAt
	}
	return e.file.UpdatedAt
}

func (e listEntry) name() string {
	if e.folder != nil {
		return e.folder.DisplayName
	}
	return e.file.DisplayName
}

func (e listEntry) id() int {
	if e.folder != nil {
		return e.folder.ID
	}
	return e.file.ID
}

// kindRank orders folders before files when entries otherwise tie.
func (e listEntry) kindRank() int {
	if e.folder != nil {
		return 0
	}
	return 1
}

// limitAndSortEntries keeps the limit most recently updated entries (all of
// them if limit is 0), then sorts them by sortBy.
func limitAndSortEntries(entries []listEntry, limit int, sortBy string, reverse bool) []listEntry {
	if limit > 0 && len(entries) > limit {
		sortEntries(entries, "time", true)
		entries = entries[:limit]
	}
	sortEntries(entries, sortBy, reverse)
	return entries
}

// sortEntries sorts entries like 'ls -ltr': by last update with the oldest
// first, or by name when sortBy is "name". reverse inverts the order. Kind
// (folders first) and then ID break ties so the order is stable.
func sortEntries(entries []listEntry, sortBy string, reverse bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if reverse {
			a, b = b, a
		}
		switch sortBy {
		case "name":
			if an, bn := strings.ToLower(a.name()), strings.ToLower(b.name()); an != bn {
				return an < bn
			}
		default:
			if !a.updated().Equal(b.updated()) {
				return a.updated().Before(b.updated())
			}
		}
		if a.kindRank() != b.kindRank() {
			return a.kindRank() < b.kindRank()
		}
		return a.id() < b.id()
	})
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

			return formatAndPrint(cmd, &FolderCreatedView{Folder: folder})
		},
	}

	cmd.Flags().IntVarP(&parentID, "parent", "p", 0, "Parent folder ID")
	return cmd
}

func newUploadCmd() *cobra.Command {
	var folderID int
	var name string

	cmd := &cobra.Command{
		Use:   "upload <PATH> [PATH...]",
		Short: "Upload files and directories",
		Long: `Upload local files and directories to an OpenRelik folder using resumable
chunked uploads with progress tracking.

Directories are uploaded recursively up to --depth levels deep, and each
one is recreated as a folder of the same name in the destination.

With --folder, uploads go into that existing folder. Without it, a new
root folder is created: for a single directory it is named after the
directory and holds its contents; otherwise it is named after the current
time, e.g. "Upload 2026-10-27 14:05". Use --name to choose the name.`,
		Example: `  # Upload a file to folder 42
  openrelik folder upload report.pdf --folder 42

  # Upload several files to folder 42
  openrelik folder upload *.E01 --folder 42

  # Upload a directory into a new root folder named "evidence"
  openrelik folder upload ./evidence/

  # Upload files into a new root folder with a chosen name
  openrelik folder upload disk.E01 memory.raw --name "Case 2026-001"

  # Use a larger chunk size (bytes) for fast networks
  openrelik folder upload large.bin --folder 42 --chunk-size 8388608`,
		Args:         util.UseArgs(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("folder") && folderID <= 0 {
				return fmt.Errorf("invalid folder ID: %d", folderID)
			}

			// Check every path up front so nothing is created for a typo.
			isDir := make([]bool, len(args))
			for i, p := range args {
				info, err := os.Stat(p)
				if err != nil {
					return err
				}
				isDir[i] = info.IsDir()
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			view := &UploadView{FolderID: folderID}
			if folderID == 0 {
				// A single directory becomes the new root folder itself.
				single := len(args) == 1 && isDir[0]
				if name == "" {
					name = defaultUploadFolderName(args, isDir, time.Now())
				}
				folder, _, err := client.Folders().CreateRootFolder(cmd.Context(), name)
				if err != nil {
					return fmt.Errorf("failed to create root folder: %w", err)
				}
				view.Created = folder
				view.FolderID = folder.ID
				if single {
					view.Uploads, err = uploadDirContents(cmd.Context(), cmd.ErrOrStderr(), client, args[0], folder.ID, 0)
					if err != nil {
						return err
					}
					return formatAndPrint(cmd, view)
				}
			}

			for i, p := range args {
				var uploads []uploadedFile
				if isDir[i] {
					uploads, err = uploadDir(cmd.Context(), cmd.ErrOrStderr(), client, p, view.FolderID)
				} else {
					var file *openrelik.File
					file, err = uploadFileWithProgress(cmd.Context(), cmd.ErrOrStderr(), client, p, view.FolderID)
					uploads = []uploadedFile{{File: file, FolderID: view.FolderID}}
				}
				if err != nil {
					return err
				}
				view.Uploads = append(view.Uploads, uploads...)
			}
			return formatAndPrint(cmd, view)
		},
	}

	cmd.Flags().IntVar(&folderID, "folder", 0, "ID of an existing folder to upload into (default: create a new root folder)")
	cmd.Flags().StringVar(&name, "name", "", "Name of the new root folder")
	cmd.Flags().IntVarP(&maxDepth, "depth", "d", 3, "Maximum subfolder depth to upload")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 4*1024*1024, "Chunk size in bytes")
	cmd.MarkFlagsMutuallyExclusive("folder", "name")
	return cmd
}

// defaultUploadFolderName names the root folder created by 'folder upload':
// a single directory keeps its own name, anything else gets a timestamp.
func defaultUploadFolderName(paths []string, isDir []bool, now time.Time) string {
	if len(paths) == 1 && isDir[0] {
		return filepath.Base(filepath.Clean(paths[0]))
	}
	return "Upload " + now.Format("2006-01-02 15:04")
}

// uploadedFile is a file uploaded by 'folder upload' and the folder it went to.
type uploadedFile struct {
	File     *openrelik.File `json:"file"`
	FolderID int             `json:"folder_id"`
}

// uploadDir recreates localPath as a subfolder of remoteFolderID and uploads
// its contents into it.
func uploadDir(ctx context.Context, out io.Writer, client *openrelik.Client, localPath string, remoteFolderID int) ([]uploadedFile, error) {
	dirName := filepath.Base(filepath.Clean(localPath))
	folder, _, err := client.Folders().CreateSubFolder(ctx, remoteFolderID, dirName)
	if err != nil {
		return nil, fmt.Errorf("failed to create subfolder %s: %w", dirName, err)
	}
	return uploadDirContents(ctx, out, client, localPath, folder.ID, 0)
}

// uploadDirContents uploads the contents of localPath into remoteFolderID, recreating
// subdirectories as subfolders up to maxDepth levels deep.
func uploadDirContents(ctx context.Context, out io.Writer, client *openrelik.Client, localPath string, remoteFolderID int, depth int) ([]uploadedFile, error) {
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", localPath, err)
	}

	var uploads []uploadedFile
	for _, entry := range entries {
		entryPath := filepath.Join(localPath, entry.Name())
		if entry.IsDir() {
			if depth >= maxDepth {
				continue
			}
			folder, _, err := client.Folders().CreateSubFolder(ctx, remoteFolderID, entry.Name())
			if err != nil {
				return nil, fmt.Errorf("failed to create subfolder %s: %w", entry.Name(), err)
			}
			sub, err := uploadDirContents(ctx, out, client, entryPath, folder.ID, depth+1)
			if err != nil {
				return nil, err
			}
			uploads = append(uploads, sub...)
		} else {
			file, err := uploadFileWithProgress(ctx, out, client, entryPath, remoteFolderID)
			if err != nil {
				return nil, err
			}
			uploads = append(uploads, uploadedFile{File: file, FolderID: remoteFolderID})
		}
	}
	return uploads, nil
}
