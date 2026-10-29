// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package util

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/openrelik/openrelik-go-client"
)

// fileIDPattern matches inputs that are file IDs rather than local paths.
var fileIDPattern = regexp.MustCompile(`^[0-9]+$`)

// CheckInputs checks that every input to 'run' is a file ID, a local file
// or has the form of a server path, without contacting the server or
// uploading anything, so that mistakes are caught before a workflow is
// created or previewed.
func CheckInputs(args []string) error {
	for _, arg := range args {
		if _, err := classifyInput(arg); err != nil {
			return err
		}
	}
	return nil
}

// InputError is an input to 'run' that is neither a file ID, a local file
// nor a server path. It is a mistake in the command line.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

func inputErrorf(format string, a ...any) error {
	return &InputError{fmt.Sprintf(format, a...)}
}

// inputKind is how an input to 'run' is read.
type inputKind int

const (
	inputID     inputKind = iota // a file ID
	inputLocal                   // a local file, to upload
	inputServer                  // a path of a file on the server
)

// input is an input to 'run', classified by classifyInput.
type input struct {
	kind inputKind
	id   int
	path string
}

// classifyInput reads an input made only of digits as a file ID, an
// existing local file as a local file, and any other input with a '/' as
// the path of a file on the server. A number that is also the name of a
// local file is an error rather than a guess: picking either one could run
// the workflow on the wrong evidence. Local paths can be written as ./NAME
// to make them unambiguous.
func classifyInput(arg string) (input, error) {
	info, statErr := os.Stat(arg)
	if fileIDPattern.MatchString(arg) {
		id, err := strconv.Atoi(arg)
		if err != nil || id == 0 {
			return input{}, inputErrorf("invalid file ID: %s", arg)
		}
		if statErr == nil && !info.IsDir() {
			return input{}, inputErrorf("%q is both a file ID and a local file\nUse ./%s to upload the local file, or run from a directory without it to use file %d.", arg, arg, id)
		}
		return input{kind: inputID, id: id}, nil
	}
	switch {
	case os.IsNotExist(statErr) && strings.Contains(arg, "/") && !strings.HasSuffix(arg, "/"):
		return input{kind: inputServer, path: arg}, nil
	case os.IsNotExist(statErr):
		return input{}, inputErrorf("%q is neither a file ID, a local file nor a server path (FOLDER/NAME)", arg)
	case statErr != nil:
		return input{}, fmt.Errorf("failed to stat file %s: %w", arg, statErr)
	case info.IsDir():
		return input{}, inputErrorf("%q is a directory; give files or file IDs (upload directories with 'openrelik upload')", arg)
	}
	return input{kind: inputLocal, path: arg}, nil
}

// mayBeServerPath reports whether a local path could also be read as the
// path of a file on the server: a relative path with a folder in it, not
// written as ./PATH or ../PATH.
func mayBeServerPath(path string) bool {
	return strings.Contains(path, "/") && !filepath.IsAbs(path) &&
		!strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "../")
}

// ServerPathResolver returns the ID of the file on the server at path. If
// no file has that path, the error is a *NotOnServerError.
type ServerPathResolver func(ctx context.Context, path string) (int, error)

// NotOnServerError is returned by a ServerPathResolver when no file on the
// server has the path. Err says why.
type NotOnServerError struct {
	Err error
}

func (e *NotOnServerError) Error() string { return e.Err.Error() }
func (e *NotOnServerError) Unwrap() error { return e.Err }

// ResolveInputs returns the file IDs of the inputs to 'run', in the order
// given. Inputs are file IDs, paths of files on the server, resolved with
// resolve, or local files. File IDs are checked with checkID, if it isn't
// nil, before anything is uploaded. Local files are uploaded to an OpenRelik folder,
// unless the folder already holds a file with the same name and SHA256.
// It also returns the number of bytes uploaded.
func ResolveInputs(ctx context.Context, client *openrelik.Client, resolve ServerPathResolver, checkID func(context.Context, int) error, args []string, showProgress bool, folderID int, folderName string) ([]int, int64, error) {
	fileIDs := make([]int, len(args))
	var toUpload []int // indexes into args
	var totalUploaded int64

	for i, arg := range args {
		in, err := classifyInput(arg)
		if err != nil {
			return nil, 0, err
		}
		switch in.kind {
		case inputID:
			if checkID != nil {
				if err := checkID(ctx, in.id); err != nil {
					return nil, 0, err
				}
			}
			fileIDs[i] = in.id
		case inputServer:
			id, err := resolve(ctx, in.path)
			var notOnServer *NotOnServerError
			if errors.As(err, &notOnServer) {
				return nil, 0, fmt.Errorf("%q is neither a local file nor a file on the server: %w", arg, err)
			}
			if err != nil {
				return nil, 0, err
			}
			fileIDs[i] = id
		case inputLocal:
			if mayBeServerPath(in.path) {
				id, err := resolve(ctx, in.path)
				var notOnServer *NotOnServerError
				switch {
				case err == nil:
					return nil, 0, inputErrorf("%q is both a local file and file %d on the server\nUse ./%s to upload the local file, or %d to use the server file.", arg, id, arg, id)
				case !errors.As(err, &notOnServer):
					return nil, 0, fmt.Errorf("%q is a local file, but may also be on the server (use ./%s for the local file): %w", arg, arg, err)
				}
			}
			toUpload = append(toUpload, i)
		}
	}
	if len(toUpload) == 0 {
		return fileIDs, 0, nil
	}

	var folder *openrelik.Folder
	if folderID > 0 {
		folder = &openrelik.Folder{ID: folderID}
	} else {
		if folderName == "" {
			user, _, err := client.Users().GetMe(ctx)
			if err != nil {
				return nil, 0, fmt.Errorf("failed to get current user: %w", err)
			}
			folderName = fmt.Sprintf("CLI Uploads (%s)", user.Username)
		}
		var err error
		folder, err = GetOrCreateFolder(ctx, client, folderName)
		if err != nil {
			return nil, 0, err
		}
	}

	// The same local file given twice, perhaps as a.bin and ./a.bin, is
	// uploaded once.
	var paths []string
	var sizes []int64
	sameAs := map[int]int{} // index into args -> index of its first occurrence
	firstOf := map[string]int{}
	for _, i := range toUpload {
		abs, err := filepath.Abs(args[i])
		if err != nil {
			return nil, 0, fmt.Errorf("failed to resolve path %s: %w", args[i], err)
		}
		if first, ok := firstOf[abs]; ok {
			sameAs[i] = first
			continue
		}
		firstOf[abs] = i
		info, err := os.Stat(args[i])
		if err != nil {
			return nil, 0, fmt.Errorf("failed to stat file %s: %w", args[i], err)
		}
		paths, sizes = append(paths, args[i]), append(sizes, info.Size())
	}

	// Reuse the files the folder already holds.
	existing, _, err := FindExisting(ctx, client, folder.ID, paths, make([]bool, len(paths)), sizes)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to check folder %d for files already uploaded: %w", folder.ID, err)
	}
	reused := map[string]int{}
	for _, e := range existing {
		reused[e.Path] = e.FileID
		if showProgress {
			fmt.Fprintf(os.Stderr, "Using file %d already in folder %d (%s): %s\n", e.FileID, e.FolderID, e.Reason, filepath.Base(e.Path))
		}
	}

	for _, i := range toUpload {
		path := args[i]
		if first, ok := sameAs[i]; ok {
			fileIDs[i] = fileIDs[first]
			continue
		}
		if id, ok := reused[path]; ok {
			fileIDs[i] = id
			continue
		}
		err := func() error {
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			defer f.Close()

			stat, err := f.Stat()
			if err != nil {
				return fmt.Errorf("failed to stat file %s: %w", path, err)
			}
			filename := filepath.Base(path)

			var trackerWriter io.Writer = os.Stderr
			if !showProgress {
				trackerWriter = nil
			}

			action := "Upload: " + filename
			tracker := NewProgressTracker(trackerWriter, stat.Size(), action)
			progress := func(bytesSent, totalBytes int64) {
				tracker.Update(bytesSent)
			}

			// Hash the data as it is sent, to compare with the server's hash.
			h := sha256.New()
			uploadedFile, _, err := client.Files().Upload(ctx, folder.ID, filename, io.TeeReader(f, h),
				openrelik.WithTotalSize(stat.Size()), openrelik.WithUploadProgress(progress))
			if err != nil {
				tracker.Finish()
				return fmt.Errorf("failed to upload file %s: %w", path, err)
			}
			tracker.Finish()
			if _, err := VerifyUpload(ctx, client, uploadedFile, hex.EncodeToString(h.Sum(nil))); err != nil {
				return fmt.Errorf("upload of %s: %w", path, err)
			}
			fileIDs[i] = uploadedFile.ID
			totalUploaded += stat.Size()
			return nil
		}()
		if err != nil {
			return nil, 0, err
		}
	}

	return fileIDs, totalUploaded, nil
}

// ExistingFile is a local file that a folder on the server already holds.
type ExistingFile struct {
	Path     string `json:"path"`
	FileID   int    `json:"file_id"`
	FolderID int    `json:"folder_id"`
	Reason   string `json:"reason"`
}

// FindExisting returns the files among sources that folderID already
// holds: files with the same name and SHA256. Only files with the same name
// and size are compared, so other files cost nothing beyond one listing of
// the folder. Files the server has no hash for never match: hashing is
// skipped for very large files and runs in the background, so they are
// uploaded again rather than skipped on name and size alone. They are
// returned as unhashed, so the caller can say the folder may now hold two
// copies. Directories (isDir) are left out.
func FindExisting(ctx context.Context, client *openrelik.Client, folderID int, sources []string, isDir []bool, sizes []int64) (duplicates, unhashed []ExistingFile, err error) {
	if !slices.Contains(isDir, false) {
		return nil, nil, nil // only directories, which go into new subfolders
	}
	existing, _, err := client.Folders().ListFiles(ctx, folderID)
	if err != nil {
		return nil, nil, err
	}

	for i, p := range sources {
		if isDir[i] {
			continue
		}
		var localHash string
		var noHash *ExistingFile
		for _, f := range existing {
			if f.DisplayName != filepath.Base(p) || f.Filesize != sizes[i] {
				continue
			}
			info, _, err := client.Files().Info(ctx, f.ID)
			if err != nil {
				return nil, nil, err
			}
			if info.HashSHA256 == "" {
				if noHash == nil {
					noHash = &ExistingFile{Path: p, FileID: f.ID, FolderID: folderID,
						Reason: "same name and size; the server has no hash to compare"}
				}
				continue
			}
			if localHash == "" {
				if localHash, err = FileSHA256(p); err != nil {
					return nil, nil, err
				}
			}
			if strings.EqualFold(localHash, info.HashSHA256) {
				duplicates = append(duplicates, ExistingFile{Path: p, FileID: f.ID, FolderID: folderID, Reason: "same name and SHA256"})
				noHash = nil
				break
			}
		}
		if noHash != nil {
			unhashed = append(unhashed, *noHash)
		}
	}
	return duplicates, unhashed, nil
}

// FileSHA256 returns the hex SHA256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadResults fetches the full workflow details and downloads the requested output files.
// It returns the files it saved and the workflow, which callers also print.
func DownloadResults(ctx context.Context, client *openrelik.Client, workflowID int, policy string, taskDownloadPrefs map[string]bool, outputDir string, taskFolders bool, showProgress bool) ([]*SavedFile, *openrelik.Workflow, error) {
	var saved []*SavedFile

	hasPositiveOverride := false
	for _, v := range taskDownloadPrefs {
		if v {
			hasPositiveOverride = true
			break
		}
	}

	// The status endpoint doesn't include output_files; fetch the full workflow.
	fullWorkflow, _, err := client.Workflows().Get(ctx, workflowID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch workflow details: %w", err)
	}

	if policy == "none" && !hasPositiveOverride {
		return nil, fullWorkflow, nil
	}

	tasksToDownload := []openrelik.Task{}
	allTasks := FlattenTasks(fullWorkflow.Tasks)

	var final map[string]bool
	if policy == "final" {
		var err error
		if final, err = FinalTasks(fullWorkflow.SpecJSON, allTasks); err != nil {
			return nil, fullWorkflow, err
		}
	}

	for _, task := range allTasks {
		shouldDownload := false

		// Check per-task preference first
		if pref, ok := taskDownloadPrefs[task.UUID]; ok {
			shouldDownload = pref
		} else {
			// Fallback to global policy
			if policy == "all" {
				shouldDownload = true
			} else if policy == "final" {
				shouldDownload = final[task.UUID]
			}
		}

		if shouldDownload {
			tasksToDownload = append(tasksToDownload, task)
		}
	}

	// Work out every destination before downloading anything, so that
	// names that can't be used or that collide stop the run up front.
	// Names come from the server, and often from the evidence itself, so
	// they never place a file outside outputDir and never replace a file.
	type download struct {
		fileID   int
		destPath string
	}
	var downloads []download
	seen := map[string]string{} // destination -> task that produces it
	for _, task := range tasksToDownload {
		destDir := outputDir
		if taskFolders {
			folderName, err := LocalFileName(fmt.Sprintf("%s_%d", strings.ReplaceAll(task.DisplayName, " ", "_"), task.ID))
			if err != nil {
				return nil, nil, fmt.Errorf("task %d: %w", task.ID, err)
			}
			destDir = filepath.Join(outputDir, folderName)
		}
		for _, outputFile := range task.OutputFiles {
			name, err := LocalFileName(outputFile.DisplayName)
			if err != nil {
				return nil, nil, fmt.Errorf("output file %d of task %q: %w", outputFile.ID, task.DisplayName, err)
			}
			destPath := filepath.Join(destDir, name)
			if other, ok := seen[destPath]; ok {
				return nil, nil, fmt.Errorf("tasks %q and %q both produce %q; use --task-folders to keep them apart", other, task.DisplayName, destPath)
			}
			seen[destPath] = task.DisplayName
			if _, err := os.Lstat(destPath); err == nil {
				return nil, nil, fmt.Errorf("%q already exists; move it away or download to another directory", destPath)
			}
			downloads = append(downloads, download{outputFile.ID, destPath})
		}
	}

	var progress io.Writer
	if showProgress {
		progress = os.Stderr
	}
	for _, d := range downloads {
		if err := os.MkdirAll(filepath.Dir(d.destPath), 0755); err != nil {
			return nil, nil, fmt.Errorf("failed to create task folder: %w", err)
		}
		// Output files in the workflow don't include hashes.
		file, _, err := client.Files().Info(ctx, d.fileID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get output file %d: %w", d.fileID, err)
		}
		f, err := SaveFile(ctx, client, file, d.destPath, false, progress)
		if err != nil {
			return saved, nil, err
		}
		saved = append(saved, f)
	}

	return saved, fullWorkflow, nil
}

// LocalFileName turns a file's display name from the server into a single
// local file name, dropping any directories so that a name such as
// "../../.bashrc" or "/usr/bin/ls" can't write outside the target directory.
func LocalFileName(displayName string) (string, error) {
	name := filepath.Base(displayName)
	if name == "." || !filepath.IsLocal(name) {
		return "", fmt.Errorf("name %q can't be used as a local file name", displayName)
	}
	return name, nil
}

// SavedFile is a file written by SaveFile.
type SavedFile struct {
	FileID int    `json:"file_id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Verified is true if SHA256 matched the server's hash. The server
	// doesn't hash very large files, so those can't be verified.
	Verified bool `json:"verified"`
}

// SaveFile downloads file to destPath. The data is written to a temporary
// file in the same directory, which is moved to destPath only once it is
// complete and its SHA256 matches the server's, so an interrupted or
// corrupted download never leaves a partial file at destPath or replaces
// what was there. Unless overwrite is set, an existing destPath is never
// replaced; the error then wraps fs.ErrExist. Progress is shown on progress
// unless it is nil.
func SaveFile(ctx context.Context, client *openrelik.Client, file *openrelik.File, destPath string, overwrite bool, progress io.Writer) (*SavedFile, error) {
	tmp, err := createPartial(destPath)
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	body, _, err := client.Files().Download(ctx, file.ID)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	defer body.Close()

	var r io.Reader = body
	if progress != nil {
		r = &ProgressReader{Reader: body, Tracker: NewProgressTracker(progress, file.Filesize, "Download: "+file.DisplayName)}
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("failed to download file %d: %w", file.ID, err)
	}

	saved := &SavedFile{FileID: file.ID, Path: destPath, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	if file.HashSHA256 != "" {
		if !strings.EqualFold(saved.SHA256, file.HashSHA256) {
			return nil, fmt.Errorf("file %d was not saved: its SHA256 on the server is %s, but the downloaded data has %s", file.ID, file.HashSHA256, saved.SHA256)
		}
		saved.Verified = true
	} else if file.Filesize > 0 && n != file.Filesize {
		return nil, fmt.Errorf("file %d was not saved: it is %d bytes on the server, but %d bytes were downloaded", file.ID, file.Filesize, n)
	}

	if err := placeFile(tmpPath, destPath, overwrite); err != nil {
		return nil, err
	}
	tmpPath = ""
	return saved, nil
}

// createPartial creates a new, hidden temporary file next to destPath. Unlike
// os.CreateTemp it lets the umask decide the permissions, as for any other
// new file.
func createPartial(destPath string) (*os.File, error) {
	for range 100 {
		name := filepath.Join(filepath.Dir(destPath), fmt.Sprintf(".%s.%d.partial", filepath.Base(destPath), rand.Uint32()))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		if !errors.Is(err, fs.ErrExist) {
			return f, err
		}
	}
	return nil, fmt.Errorf("failed to create a temporary file next to %q", destPath)
}

// placeFile moves the file at tmpPath to destPath. Unless overwrite is set,
// it fails if destPath exists, including when something appeared there after
// the caller checked.
func placeFile(tmpPath, destPath string, overwrite bool) error {
	if overwrite {
		return os.Rename(tmpPath, destPath)
	}
	// A hard link is never created over an existing file, symlink or not.
	err := os.Link(tmpPath, destPath)
	if err == nil {
		os.Remove(tmpPath)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%q already exists: %w", destPath, fs.ErrExist)
	}
	// The filesystem may not support hard links.
	if _, err := os.Lstat(destPath); err == nil {
		return fmt.Errorf("%q already exists: %w", destPath, fs.ErrExist)
	}
	return os.Rename(tmpPath, destPath)
}

// VerifyUpload compares localSHA256, the hash of the data sent for an
// upload, with the hash the server computed for the uploaded file. It
// returns false without an error if the server has no hash to compare: very
// large files aren't hashed, and hashing may still be running. It fails if
// the hash can't be asked for.
func VerifyUpload(ctx context.Context, client *openrelik.Client, file *openrelik.File, localSHA256 string) (bool, error) {
	serverSHA256 := file.HashSHA256
	if serverSHA256 == "" {
		info, _, err := client.Files().Info(ctx, file.ID)
		if err != nil {
			return false, fmt.Errorf("could not get the SHA256 of file %d from the server to verify the upload: %w", file.ID, err)
		}
		serverSHA256 = info.HashSHA256
	}
	if serverSHA256 == "" {
		return false, nil
	}
	if !strings.EqualFold(serverSHA256, localSHA256) {
		return false, fmt.Errorf("file %d on the server doesn't match what was uploaded: its SHA256 is %s, but the local file's is %s", file.ID, serverSHA256, localSHA256)
	}
	return true, nil
}
