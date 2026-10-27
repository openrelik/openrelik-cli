package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

var (
	chunkSize int
)

func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Manage files",
		Long:  `Download and inspect files stored in OpenRelik.`,
	}

	cmd.AddCommand(newFileInfoCmd())
	cmd.AddCommand(newFileDownloadCmd())
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

func newFileDownloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "download <ID> [DESTINATION]",
		Short: "Download a file",
		Long: `Download a file from OpenRelik to the local filesystem.

ID is the integer file ID. DESTINATION may be a file path or an existing
directory; if a directory is given, the original filename is used inside it.
If DESTINATION is omitted, the file is saved to the current directory using
its original filename. You will be prompted before overwriting an existing file.`,
		Example: `  # Download to current directory (original filename)
  openrelik file download 123

  # Download to a specific path
  openrelik file download 123 ./output/report.pdf

  # Download into a directory (uses original filename)
  openrelik file download 123 ./output/`,
		Args:         util.UseArgs(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fileID, err := strconv.Atoi(args[0])
			if err != nil {
				return err
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

			// Determine destination path
			destPath := file.DisplayName
			if len(args) > 1 {
				destPath = args[1]
				info, err := os.Stat(destPath)
				if err == nil && info.IsDir() {
					destPath = filepath.Join(destPath, file.DisplayName)
				}
			}

			// Check if folder exists
			parentDir := filepath.Dir(destPath)
			if _, err := os.Stat(parentDir); os.IsNotExist(err) {
				return fmt.Errorf("destination folder %q does not exist", parentDir)
			}

			// Check for overwrite
			if _, err := os.Stat(destPath); err == nil {
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

			out, err := os.Create(destPath)
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
}

// uploadFileWithProgress uploads a single local file to the given folder,
// showing a progress tracker if quiet mode is off. It uses the package-level
// chunkSize variable for chunked uploads.
func uploadFileWithProgress(ctx context.Context, out io.Writer, client *openrelik.Client, filePath string, folderID int) (*openrelik.File, error) {
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
		tracker = util.NewProgressTracker(out, fileInfo.Size(), "Upload: "+filepath.Base(filePath))
		totalChunks := int(fileInfo.Size() / int64(chunkSize))
		if fileInfo.Size()%int64(chunkSize) != 0 {
			totalChunks++
		}
		if totalChunks == 0 {
			totalChunks = 1
		}
		tracker.SetTotalChunks(totalChunks)
	}

	opts := []openrelik.UploadOption{
		openrelik.WithChunkSize(chunkSize),
	}

	lastChunkNum := 0
	if tracker != nil {
		opts = append(opts, openrelik.WithUploadProgress(func(bytesSent, totalBytes int64) {
			currentChunk := int(bytesSent / int64(chunkSize))
			if bytesSent%int64(chunkSize) != 0 {
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

	result, _, err := client.Files().Upload(ctx, folderID, filepath.Base(filePath), file, opts...)
	if tracker != nil {
		tracker.Finish()
	}
	return result, err
}
