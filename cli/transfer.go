package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

// Top-level commands that move data between the local machine and OpenRelik.

func newUploadCmd() *cobra.Command {
	var folder string
	var name string
	var chunkSize, maxDepth int

	cmd := &cobra.Command{
		Use:   "upload <PATH> [PATH...]",
		Short: "Upload files and directories",
		Long: `Upload local files and directories to an OpenRelik folder using resumable
chunked uploads with progress tracking.

Directories are uploaded recursively up to --depth levels deep, and each
one is recreated as a folder of the same name in the destination.

With --folder, uploads go into that existing folder, given as a folder ID
or a path of folder names as accepted by 'folder list'. Without it, a new
root folder is created: for a single directory it is named after the
directory and holds its contents; otherwise it is named after the current
time, e.g. "Upload 2026-10-27 14:05". Use --name to choose the name.`,
		Example: `  # Upload a file to folder 42
  openrelik upload report.pdf --folder 42

  # Upload several files to folder 42
  openrelik upload *.E01 --folder 42

  # Upload to a folder by its path
  openrelik upload disk.E01 --folder "Case A/evidence/"

  # Upload a directory into a new root folder named "evidence"
  openrelik upload ./evidence/

  # Upload files into a new root folder with a chosen name
  openrelik upload disk.E01 memory.raw --name "Case 2026-001"

  # Use a larger chunk size (bytes) for fast networks
  openrelik upload large.bin --folder 42 --chunk-size 8388608`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxDepth < 0 {
				return fmt.Errorf("invalid depth: %d (must be 0 or greater)", maxDepth)
			}
			if chunkSize <= 0 {
				return fmt.Errorf("invalid chunk size: %d (must be greater than 0)", chunkSize)
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

			up := &uploader{client: client, out: cmd.ErrOrStderr(), chunkSize: chunkSize, maxDepth: maxDepth}
			view := &UploadView{}
			// A single directory becomes the new root folder itself.
			single := false
			if cmd.Flags().Changed("folder") {
				view.FolderID, err = resolveFolder(cmd.Context(), client, folder)
				if err != nil {
					return err
				}
			} else {
				single = len(args) == 1 && isDir[0]
				if name == "" {
					name = defaultUploadFolderName(args, isDir, time.Now())
				}
				created, _, err := client.Folders().CreateRootFolder(cmd.Context(), name)
				if err != nil {
					return fmt.Errorf("failed to create root folder: %w", err)
				}
				view.Created = created
				view.FolderID = created.ID
			}

			upload := func() error {
				if single {
					view.Uploads, err = up.uploadDirContents(cmd.Context(), args[0], view.FolderID, 0)
					return err
				}
				for i, p := range args {
					var uploads []uploadedFile
					if isDir[i] {
						uploads, err = up.uploadDir(cmd.Context(), p, view.FolderID)
					} else {
						var file *openrelik.File
						file, err = up.uploadFile(cmd.Context(), p, view.FolderID)
						uploads = []uploadedFile{{File: file, FolderID: view.FolderID}}
					}
					if err != nil {
						return err
					}
					view.Uploads = append(view.Uploads, uploads...)
				}
				return nil
			}
			if err := upload(); err != nil {
				// The view is never printed, so say where the partial upload went.
				if view.Created != nil {
					return fmt.Errorf("%w\n\nFolder %q (ID %d) was created and holds the files uploaded before the error.",
						err, view.Created.DisplayName, view.Created.ID)
				}
				return err
			}
			return formatAndPrint(cmd, view)
		},
	}

	cmd.Flags().StringVar(&folder, "folder", "", "ID or path of an existing folder to upload into (default: create a new root folder)")
	cmd.Flags().StringVar(&name, "name", "", "Name of the new root folder")
	cmd.Flags().IntVarP(&maxDepth, "depth", "d", 3, "Maximum subfolder depth to upload")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 4*1024*1024, "Chunk size in bytes")
	cmd.MarkFlagsMutuallyExclusive("folder", "name")
	return cmd
}

// defaultUploadFolderName names the root folder created by 'upload':
// a single directory keeps its own name, anything else gets a timestamp.
func defaultUploadFolderName(paths []string, isDir []bool, now time.Time) string {
	if len(paths) == 1 && isDir[0] {
		return localDirName(paths[0])
	}
	return "Upload " + now.Format("2006-01-02 15:04")
}

// localDirName returns the name of the directory at path, resolving "." and
// ".." so that 'upload .' uses the directory's real name.
func localDirName(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Base(path)
}

// uploadedFile is a file uploaded by 'upload' and the folder it went to.
type uploadedFile struct {
	File     *openrelik.File `json:"file"`
	FolderID int             `json:"folder_id"`
}

// uploader uploads local files and directories with the settings of one
// 'upload' run.
type uploader struct {
	client    *openrelik.Client
	out       io.Writer // where progress is shown
	chunkSize int
	maxDepth  int
}

// uploadDir recreates localPath as a subfolder of remoteFolderID and uploads
// its contents into it.
func (u *uploader) uploadDir(ctx context.Context, localPath string, remoteFolderID int) ([]uploadedFile, error) {
	dirName := localDirName(localPath)
	folder, _, err := u.client.Folders().CreateSubFolder(ctx, remoteFolderID, dirName)
	if err != nil {
		return nil, fmt.Errorf("failed to create subfolder %s: %w", dirName, err)
	}
	return u.uploadDirContents(ctx, localPath, folder.ID, 0)
}

// uploadDirContents uploads the contents of localPath into remoteFolderID, recreating
// subdirectories as subfolders up to u.maxDepth levels deep.
func (u *uploader) uploadDirContents(ctx context.Context, localPath string, remoteFolderID int, depth int) ([]uploadedFile, error) {
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", localPath, err)
	}

	var uploads []uploadedFile
	for _, entry := range entries {
		entryPath := filepath.Join(localPath, entry.Name())
		if entry.IsDir() {
			if depth >= u.maxDepth {
				continue
			}
			folder, _, err := u.client.Folders().CreateSubFolder(ctx, remoteFolderID, entry.Name())
			if err != nil {
				return nil, fmt.Errorf("failed to create subfolder %s: %w", entry.Name(), err)
			}
			sub, err := u.uploadDirContents(ctx, entryPath, folder.ID, depth+1)
			if err != nil {
				return nil, err
			}
			uploads = append(uploads, sub...)
		} else {
			file, err := u.uploadFile(ctx, entryPath, remoteFolderID)
			if err != nil {
				return nil, err
			}
			uploads = append(uploads, uploadedFile{File: file, FolderID: remoteFolderID})
		}
	}
	return uploads, nil
}

func newDownloadCmd() *cobra.Command {
	var fileID int

	cmd := &cobra.Command{
		Use:   "download [DESTINATION]",
		Short: "Download a file",
		Long: `Download a file from OpenRelik to the local filesystem.

--file is the integer file ID, as shown by 'folder list'. DESTINATION may
be a file path or an existing directory; if a directory is given, the
original filename is used inside it.
If DESTINATION is omitted, the file is saved to the current directory using
its original filename. You will be prompted before overwriting an existing file.

The original filename never places the file outside the current or given
directory: any directories in it are dropped, and existing symlinks are not
followed. If the name can't be used, give DESTINATION as a file path.`,
		Example: `  # Download to current directory (original filename)
  openrelik download --file 123

  # Download to a specific path
  openrelik download --file 123 ./output/report.pdf

  # Download into a directory (uses original filename)
  openrelik download --file 123 ./output/`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fileID <= 0 {
				return fmt.Errorf("invalid file ID: %d", fileID)
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			// Get file info first for filename and size
			file, _, err := client.Files().Info(cmd.Context(), fileID)
			if err != nil {
				return err
			}

			// Determine destination path. Unless the user gave a file path,
			// the name comes from the server and must stay inside the
			// current or given directory.
			var destPath string
			nameFromServer := true
			if len(args) > 0 {
				destPath = args[0]
				info, err := os.Stat(destPath)
				nameFromServer = err == nil && info.IsDir()
			}
			if nameFromServer {
				name, err := localFileName(file.DisplayName)
				if err != nil {
					return fmt.Errorf("file %d: %w; give DESTINATION as a file path to choose where to save it", fileID, err)
				}
				destPath = filepath.Join(destPath, name)
			}

			// Check if folder exists
			parentDir := filepath.Dir(destPath)
			if _, err := os.Stat(parentDir); os.IsNotExist(err) {
				return fmt.Errorf("destination folder %q does not exist", parentDir)
			}

			// Check for overwrite. Lstat so a symlink at a server-chosen
			// name can't redirect the download elsewhere.
			exists := false
			if info, err := os.Lstat(destPath); err == nil {
				if nameFromServer && info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("%q is a symlink; refusing to write through it", destPath)
				}
				exists = true
				confirmed, err := util.Confirm(cmd.OutOrStdout(), cmd.InOrStdin(), fmt.Sprintf("File %q already exists. Overwrite?", destPath))
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("cancelled")
				}
			}

			// Download file
			body, _, err := client.Files().Download(cmd.Context(), fileID)
			if err != nil {
				return err
			}
			defer body.Close()

			// O_EXCL makes creating a new file fail if something, such as a
			// symlink, appeared at destPath since the check above.
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if !exists {
				flags |= os.O_EXCL
			}
			out, err := os.OpenFile(destPath, flags, 0o666)
			if err != nil {
				return err
			}
			defer out.Close()

			var r io.Reader = body
			if !quiet {
				tracker := util.NewProgressTracker(cmd.ErrOrStderr(), file.Filesize, "Download: "+file.DisplayName)
				r = &util.ProgressReader{
					Reader:  body,
					Tracker: tracker,
				}
			}

			_, err = io.Copy(out, r)
			return err
		},
	}

	cmd.Flags().IntVar(&fileID, "file", 0, "ID of the file to download")
	cmd.MarkFlagRequired("file")
	return cmd
}

// localFileName turns a file's display name from the server into a single
// local file name, dropping any directories so that a name such as
// "../../.bashrc" or "/usr/bin/ls" can't write outside the target directory.
func localFileName(displayName string) (string, error) {
	name := filepath.Base(displayName)
	if name == "." || !filepath.IsLocal(name) {
		return "", fmt.Errorf("name %q can't be used as a local file name", displayName)
	}
	return name, nil
}

// uploadFile uploads a single local file to the given folder in chunks of
// u.chunkSize, showing a progress tracker if quiet mode is off.
func (u *uploader) uploadFile(ctx context.Context, filePath string, folderID int) (*openrelik.File, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if fileInfo.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a file", filePath)
	}

	var tracker *util.ProgressTracker
	if !quiet {
		tracker = util.NewProgressTracker(u.out, fileInfo.Size(), "Upload: "+filepath.Base(filePath))
		totalChunks := int(fileInfo.Size() / int64(u.chunkSize))
		if fileInfo.Size()%int64(u.chunkSize) != 0 {
			totalChunks++
		}
		if totalChunks == 0 {
			totalChunks = 1
		}
		tracker.SetTotalChunks(totalChunks)
	}

	opts := []openrelik.UploadOption{
		openrelik.WithChunkSize(u.chunkSize),
	}

	lastChunkNum := 0
	if tracker != nil {
		opts = append(opts, openrelik.WithUploadProgress(func(bytesSent, totalBytes int64) {
			currentChunk := int(bytesSent / int64(u.chunkSize))
			if bytesSent%int64(u.chunkSize) != 0 {
				currentChunk++
			}
			if currentChunk > lastChunkNum {
				for i := 0; i < currentChunk-lastChunkNum; i++ {
					tracker.IncrementChunk()
				}
				lastChunkNum = currentChunk
			}
			tracker.Update(bytesSent)
		}))
		opts = append(opts, openrelik.WithUploadRetry(func(chunkNum, attempt int, err error) {
			tracker.IncrementRetry()
		}))
	}

	result, _, err := u.client.Files().Upload(ctx, folderID, filepath.Base(filePath), file, opts...)
	if tracker != nil {
		tracker.Finish()
	}
	return result, err
}
