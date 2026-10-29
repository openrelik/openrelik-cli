package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

// Top-level commands that move data between the local machine and OpenRelik.

func newUploadCmd() *cobra.Command {
	var name, caseRef string
	var chunkSize, maxDepth int
	var force bool

	cmd := &cobra.Command{
		Use:   "upload <PATH> [PATH...] [FOLDER]",
		Short: "Upload files and directories",
		Long: `Upload local files and directories to an OpenRelik folder using resumable
chunked uploads with progress tracking.

With more than one argument, the last one is the existing folder to upload
into, given as a folder ID or a path of folder names as accepted by
'folder list'. Directories are uploaded recursively, and each one is
recreated as a subfolder of the same name, as with 'cp -r'. Use --depth N
to upload only N levels of subdirectories; the directories left out are
listed in the output.

With a single argument, or with "/" as FOLDER, a new root folder is
created instead, since files can't be stored directly at the root. A
single directory becomes the new root folder itself; anything else goes
into a root folder named after the current time, e.g.
"Upload 2026-10-27 14:05 UTC". Use --name to choose the name.

With --case REF, files go into the case folder of case REF (see 'case
create'), or into FOLDER inside it: a path from the case folder, or
the ID of a folder in the case. No root folder is created.

Files given directly are skipped if FOLDER already holds a file with the
same name and SHA256, so an interrupted upload can be run again. If the
server has no hash for a file with the same name and size (very large
files aren't hashed, and hashing runs in the background), you are asked
whether to upload it; without a terminal to ask on, the upload fails
instead. Use --force to upload without checking.

Each file is hashed as it is sent, and the SHA256 is compared with the one
the server computes. The upload fails if they differ. Files the server
hasn't hashed (yet) are reported as not verified.`,
		Example: `  # Upload a file to folder 42
  openrelik upload report.pdf 42

  # Upload several files to a folder by its path
  openrelik upload *.E01 "Case A/evidence/"

  # Upload a directory into a new root folder named "evidence"
  openrelik upload ./evidence/

  # Upload files into a new root folder with a chosen name
  openrelik upload disk.E01 memory.raw / --name "Case 2026-001"

  # Upload a file into the case folder of case-123
  openrelik upload disk.E01 --case case-123

  # Upload a directory into the folder "evidence" of case-123
  openrelik upload logs/ evidence/ --case case-123

  # Use a larger chunk size (bytes) for fast networks
  openrelik upload large.bin 42 --chunk-size 8388608`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxDepth < 0 {
				return usageErrorf("invalid depth: %d (must be 0 or greater)", maxDepth)
			}
			if chunkSize <= 0 {
				return usageErrorf("invalid chunk size: %d (must be greater than 0)", chunkSize)
			}

			// With more than one argument, the last is the destination folder.
			sources, dest := args, ""
			if n := len(args); n > 1 {
				sources, dest = args[:n-1], args[n-1]
			}
			withCase := cmd.Flags().Changed("case")
			if withCase && dest == "/" {
				return usageErrorf(`--case uploads into the case folder, so FOLDER can't be "/"`)
			}
			if withCase && name != "" {
				return usageErrorf("--case uploads into the case folder, so it can't be used with --name")
			}
			newRoot := !withCase && (dest == "" || dest == "/")
			if name != "" && !newRoot {
				return fmt.Errorf(`--name only applies when creating a new root folder (no FOLDER, or FOLDER "/")`)
			}

			// Check every path up front so nothing is created for a typo.
			// A path given more than once, under any name, is uploaded once.
			var unique []string
			var infos []os.FileInfo
			var isDir []bool
			var sizes []int64
			for _, p := range sources {
				if abs, err := filepath.Abs(p); err == nil && filepath.Dir(abs) == abs {
					return fmt.Errorf("refusing to upload the filesystem root %q", p)
				}
				info, err := os.Stat(p)
				if err != nil {
					return err
				}
				if slices.ContainsFunc(infos, func(seen os.FileInfo) bool { return os.SameFile(seen, info) }) {
					continue
				}
				unique = append(unique, p)
				infos = append(infos, info)
				isDir = append(isDir, info.IsDir())
				sizes = append(sizes, info.Size())
			}
			sources = unique

			client, err := newClient()
			if err != nil {
				return err
			}

			up := &uploader{client: client, out: cmd.ErrOrStderr(), chunkSize: chunkSize, maxDepth: maxDepth}
			view := &UploadView{}
			// A single directory becomes the new root folder itself.
			single := false
			if !newRoot {
				var caseFolder *openrelik.Folder
				if withCase {
					if caseFolder, err = lookupCase(cmd, client, caseRef); err != nil {
						return err
					}
					view.FolderID = caseFolder.ID
				}
				if dest != "" {
					view.FolderID, err = resolveFolderIn(cmd.Context(), client, caseFolder, dest)
				}
				if err != nil {
					// The user may have left out the destination, so the
					// last file was taken as the folder.
					var notFound notFoundError
					if _, statErr := os.Stat(dest); statErr == nil && errors.As(err, &notFound) {
						fix := `Add the destination folder at the end, or "/" to create a new root folder.`
						if withCase {
							fix = "Add the destination folder at the end, or leave it out to upload into the case folder."
						}
						return fmt.Errorf("%w\n\nThe last argument is the folder to upload into, but %q is also a local path. %s", err, dest, fix)
					}
					return err
				}
				if !force {
					var unverified []skippedFile
					view.Skipped, unverified, err = util.FindExisting(cmd.Context(), client, view.FolderID, sources, isDir, sizes)
					if err != nil {
						return err
					}
					declined, err := askUnverified(cmd, unverified)
					if err != nil {
						return err
					}
					view.Skipped = append(view.Skipped, declined...)
				}
			} else {
				single = len(sources) == 1 && isDir[0]
				if name == "" {
					name = defaultUploadFolderName(sources, isDir, time.Now())
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
					view.Uploads, err = up.uploadDirContents(cmd.Context(), sources[0], view.FolderID, 0)
					return err
				}
				skipped := map[string]bool{}
				for _, s := range view.Skipped {
					skipped[s.Path] = true
				}
				for i, p := range sources {
					if skipped[p] {
						continue
					}
					var uploads []uploadedFile
					if isDir[i] {
						uploads, err = up.uploadDir(cmd.Context(), p, view.FolderID)
					} else {
						var file uploadedFile
						file, err = up.uploadFile(cmd.Context(), p, view.FolderID)
						uploads = []uploadedFile{file}
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
			view.NotUploaded = up.notUploaded
			return formatAndPrint(cmd, view)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", `Name of the new root folder (only with no FOLDER, or FOLDER "/")`)
	cmd.Flags().StringVar(&caseRef, "case", "", "Upload into the case folder of case `REF`, or FOLDER inside it")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Upload files even if FOLDER already holds the same file")
	cmd.Flags().IntVarP(&maxDepth, "depth", "d", 0, "Maximum subfolder depth to upload (0 uploads all levels)")
	cmd.Flags().IntVar(&chunkSize, "chunk-size", 4*1024*1024, "Chunk size in bytes")
	removedFlag(cmd, "folder", "openrelik upload <PATH>... <FOLDER>")
	return cmd
}

// defaultUploadFolderName names the root folder created by 'upload':
// a single directory keeps its own name, anything else gets a timestamp.
func defaultUploadFolderName(paths []string, isDir []bool, now time.Time) string {
	if len(paths) == 1 && isDir[0] {
		return localDirName(paths[0])
	}
	return "Upload " + now.UTC().Format("2006-01-02 15:04") + " UTC"
}

// localDirName returns the name of the directory at path, resolving "." and
// ".." so that 'upload .' uses the directory's real name.
func localDirName(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Base(path)
}

// skippedFile is a file 'upload' left out because the destination folder
// already holds it.
type skippedFile = util.ExistingFile

// askUnverified asks whether to upload each file that may already be in
// the destination folder, and returns the ones to skip. Without a terminal
// to ask on, it fails instead, so a script never skips a file silently.
func askUnverified(cmd *cobra.Command, unverified []skippedFile) ([]skippedFile, error) {
	if len(unverified) == 0 {
		return nil, nil
	}
	describe := func(s skippedFile) string {
		return fmt.Sprintf("%q may already be in folder %d as file %d: it has the same name and size, but the server has no hash to compare.",
			filepath.Base(s.Path), s.FolderID, s.FileID)
	}

	if !stdinIsTerminal(cmd.InOrStdin()) {
		var b strings.Builder
		for _, s := range unverified {
			b.WriteString(describe(s) + "\n")
		}
		b.WriteString("\nUse --force to upload anyway, or leave these files out.")
		return nil, errors.New(b.String())
	}

	var skip []skippedFile
	for _, s := range unverified {
		upload, err := util.Confirm(cmd.ErrOrStderr(), cmd.InOrStdin(), describe(s)+" Upload anyway?")
		if err != nil {
			return nil, err
		}
		if !upload {
			skip = append(skip, s)
		}
	}
	return skip, nil
}

// uploadedFile is a file uploaded by 'upload' and the folder it went to.
type uploadedFile struct {
	File     *openrelik.File `json:"file"`
	FolderID int             `json:"folder_id"`
	// SHA256 is the hash of the data sent. Verified is true if it matched
	// the hash the server computed for the file.
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
}

// notUploadedDir is a local directory 'upload' left out because it is
// deeper than --depth.
type notUploadedDir struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// uploader uploads local files and directories with the settings of one
// 'upload' run.
type uploader struct {
	client    *openrelik.Client
	out       io.Writer // where progress is shown
	chunkSize int
	maxDepth  int // 0 means no limit

	notUploaded []notUploadedDir // directories deeper than maxDepth
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
// subdirectories as subfolders up to u.maxDepth levels deep. Directories
// below that are recorded in u.notUploaded.
func (u *uploader) uploadDirContents(ctx context.Context, localPath string, remoteFolderID int, depth int) ([]uploadedFile, error) {
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", localPath, err)
	}

	var uploads []uploadedFile
	for _, entry := range entries {
		entryPath := filepath.Join(localPath, entry.Name())
		if entry.IsDir() {
			if u.maxDepth > 0 && depth >= u.maxDepth {
				u.notUploaded = append(u.notUploaded, notUploadedDir{
					Path:   entryPath + string(filepath.Separator),
					Reason: fmt.Sprintf("deeper than --depth %d", u.maxDepth),
				})
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
			uploads = append(uploads, file)
		}
	}
	return uploads, nil
}

func newDownloadCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "download <FILE> [DESTINATION]",
		Short: "Download a file",
		Long: `Download a file from OpenRelik to the local filesystem.

FILE is either a file ID, as shown by 'folder list <FOLDER>', or a path
of folder names ending in the file name, such as 'Case A/evidence/notes.txt'.
File names may contain '/' too: 'Case A/2024/report.txt' also finds a
file named "2024/report.txt" in Case A.
If more than one file matches a path, the matches are listed so you can
pick one by ID.

Every part of a path is a name, starting at the root folders, so
'42/notes.txt' looks in a root folder named "42", not in folder 42; use
the file ID instead.

DESTINATION may be a file path or an existing directory; if a directory is
given, the original filename is used inside it.
If DESTINATION is omitted, the file is saved to the current directory using
its original filename. You are asked before an existing file is replaced;
without a terminal to ask on, the download fails instead. Use --force to
replace it without asking.

The file is first written to a temporary file next to DESTINATION, and is
only moved into place once its SHA256 matches the server's, so a failed or
interrupted download never leaves a partial file at DESTINATION or replaces
an existing one. The SHA256 is printed; files the server hasn't hashed are
reported as not verified.

The original filename never places the file outside the current or given
directory: any directories in it are dropped, and existing symlinks are not
followed. If the name can't be used, give DESTINATION as a file path.`,
		Example: `  # Download to current directory (original filename)
  openrelik download 123

  # Download a file by its path
  openrelik download "Case A/evidence/disk.E01"

  # Download to a specific path
  openrelik download 123 ./output/report.pdf

  # Download into a directory (uses original filename)
  openrelik download 123 ./output/

  # Replace an existing file without asking
  openrelik download 123 ./output/ --force`,
		Args: func(cmd *cobra.Command, args []string) error {
			// FILE is the argument most likely to hold spaces, so don't
			// suggest quoting only DESTINATION as UseArgsWithQuoteHint would.
			err := util.UseArgs()(cmd, args)
			if err != nil && len(args) > 2 {
				return fmt.Errorf("%w, got %d\nQuote FILE and DESTINATION if they contain spaces.", err, len(args))
			}
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// By convention "-" means stdout, but download only writes
			// a file once its hash is checked.
			if len(args) > 1 && args[1] == "-" {
				return fmt.Errorf("download can't write to stdout, since it checks the SHA256 before saving; use 'openrelik file cat %s' instead, or './-' for a file named \"-\"", args[0])
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			fileID, err := resolveFile(cmd.Context(), client, args[0])
			if err != nil {
				// An unquoted path such as Case A/notes.txt splits into
				// FILE "Case" and DESTINATION "A/notes.txt".
				if len(args) > 1 && !strings.Contains(args[0], "/") && !folderIDPattern.MatchString(args[0]) {
					return fmt.Errorf("%w\nQuote FILE if it contains spaces: %q", err, strings.Join(args, " "))
				}
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
			if len(args) > 1 {
				destPath = args[1]
				info, err := os.Stat(destPath)
				nameFromServer = err == nil && info.IsDir()
			}
			if nameFromServer {
				name, err := util.LocalFileName(file.DisplayName)
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
			overwrite := false
			if info, err := os.Lstat(destPath); err == nil {
				if nameFromServer && info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("%q is a symlink; refusing to write through it", destPath)
				}
				if !force {
					if !stdinIsTerminal(cmd.InOrStdin()) {
						return fmt.Errorf("%q already exists; use --force to replace it", destPath)
					}
					confirmed, err := util.Confirm(cmd.ErrOrStderr(), cmd.InOrStdin(), fmt.Sprintf("File %q already exists. Overwrite?", destPath))
					if err != nil {
						return err
					}
					if !confirmed {
						return fmt.Errorf("cancelled; %q was not replaced", destPath)
					}
				}
				overwrite = true
			}

			var progress io.Writer
			if !quiet {
				progress = cmd.ErrOrStderr()
			}
			saved, err := util.SaveFile(cmd.Context(), client, file, destPath, overwrite, progress)
			if err != nil {
				return err
			}
			return formatAndPrint(cmd, &DownloadView{saved})
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Replace an existing file without asking")
	removedFlag(cmd, "file", "openrelik download <FILE> [DESTINATION]")
	return cmd
}

// uploadFile uploads a single local file to the given folder in chunks of
// u.chunkSize, showing a progress tracker if quiet mode is off. The data is
// hashed as it is sent and compared with the hash the server computes.
func (u *uploader) uploadFile(ctx context.Context, filePath string, folderID int) (uploadedFile, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return uploadedFile{}, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return uploadedFile{}, err
	}
	if fileInfo.IsDir() {
		return uploadedFile{}, fmt.Errorf("%s is a directory, not a file", filePath)
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
		// The hashing reader below hides the file's size from the client.
		openrelik.WithTotalSize(fileInfo.Size()),
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

	h := sha256.New()
	result, _, err := u.client.Files().Upload(ctx, folderID, filepath.Base(filePath), io.TeeReader(file, h), opts...)
	if tracker != nil {
		tracker.Finish()
	}
	if err != nil {
		return uploadedFile{}, err
	}
	uploaded := uploadedFile{File: result, FolderID: folderID, SHA256: hex.EncodeToString(h.Sum(nil))}
	uploaded.Verified, err = util.VerifyUpload(ctx, u.client, result, uploaded.SHA256)
	if err != nil {
		return uploadedFile{}, fmt.Errorf("upload of %s: %w", filePath, err)
	}
	return uploaded, nil
}
