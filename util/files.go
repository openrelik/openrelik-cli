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
	"strconv"
	"strings"

	"github.com/openrelik/openrelik-go-client"
)

// ResolveInputs takes a slice of strings that can be either file IDs (integers)
// or local file paths. It uploads local files to an OpenRelik folder.
func ResolveInputs(ctx context.Context, client *openrelik.Client, args []string, showProgress bool, folderID int, folderName string) ([]int, int64, error) {
	var fileIDs []int
	var filesToUpload []string
	var totalUploaded int64

	for _, arg := range args {
		if id, err := strconv.Atoi(arg); err == nil {
			fileIDs = append(fileIDs, id)
		} else {
			// Check if it's a local file
			if info, err := os.Stat(arg); err == nil && !info.IsDir() {
				filesToUpload = append(filesToUpload, arg)
			} else if err != nil && !os.IsNotExist(err) {
				return nil, 0, fmt.Errorf("failed to stat file %s: %w", arg, err)
			} else {
				return nil, 0, fmt.Errorf("argument %s is neither a File ID nor a local file path", arg)
			}
		}
	}

	if len(filesToUpload) > 0 {
		var folder *openrelik.Folder
		var err error

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
			folder, err = GetOrCreateFolder(ctx, client, folderName)
			if err != nil {
				return nil, 0, err
			}
		}

		for _, path := range filesToUpload {
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
				fileIDs = append(fileIDs, uploadedFile.ID)
				totalUploaded += stat.Size()
				return nil
			}()
			if err != nil {
				return nil, 0, err
			}
		}
	}

	return fileIDs, totalUploaded, nil
}

// DownloadResults fetches the full workflow details and downloads the requested output files.
func DownloadResults(ctx context.Context, client *openrelik.Client, workflowID int, policy string, taskDownloadPrefs map[string]bool, outputDir string, taskFolders bool, outputFormat string, showProgress bool) (int64, *openrelik.Workflow, error) {
	var fullWorkflow *openrelik.Workflow
	var totalDownloaded int64

	hasPositiveOverride := false
	for _, v := range taskDownloadPrefs {
		if v {
			hasPositiveOverride = true
			break
		}
	}

	if policy != "none" || outputFormat != "text" || hasPositiveOverride {
		// The status endpoint doesn't include output_files; fetch the full workflow.
		var err error
		fullWorkflow, _, err = client.Workflows().Get(ctx, workflowID)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to fetch workflow details: %w", err)
		}
	}

	if (policy == "none" && !hasPositiveOverride) || fullWorkflow == nil {
		return 0, fullWorkflow, nil
	}

	tasksToDownload := []openrelik.Task{}
	allTasks := FlattenTasks(fullWorkflow.Tasks)

	for i, task := range allTasks {
		shouldDownload := false

		// Check per-task preference first
		if pref, ok := taskDownloadPrefs[task.UUID]; ok {
			shouldDownload = pref
		} else {
			// Fallback to global policy
			if policy == "all" {
				shouldDownload = true
			} else if policy == "final" {
				shouldDownload = i == len(allTasks)-1
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
				return 0, nil, fmt.Errorf("task %d: %w", task.ID, err)
			}
			destDir = filepath.Join(outputDir, folderName)
		}
		for _, outputFile := range task.OutputFiles {
			name, err := LocalFileName(outputFile.DisplayName)
			if err != nil {
				return 0, nil, fmt.Errorf("output file %d of task %q: %w", outputFile.ID, task.DisplayName, err)
			}
			destPath := filepath.Join(destDir, name)
			if other, ok := seen[destPath]; ok {
				return 0, nil, fmt.Errorf("tasks %q and %q both produce %q; use --task-folders to keep them apart", other, task.DisplayName, destPath)
			}
			seen[destPath] = task.DisplayName
			if _, err := os.Lstat(destPath); err == nil {
				return 0, nil, fmt.Errorf("%q already exists; move it away or choose another --output-dir", destPath)
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
			return 0, nil, fmt.Errorf("failed to create task folder: %w", err)
		}
		// Output files in the workflow don't include hashes.
		file, _, err := client.Files().Info(ctx, d.fileID)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to get output file %d: %w", d.fileID, err)
		}
		saved, err := SaveFile(ctx, client, file, d.destPath, false, progress)
		if err != nil {
			return 0, nil, err
		}
		totalDownloaded += saved.Size
	}

	return totalDownloaded, fullWorkflow, nil
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
// large files aren't hashed, and hashing may still be running.
func VerifyUpload(ctx context.Context, client *openrelik.Client, file *openrelik.File, localSHA256 string) (bool, error) {
	serverSHA256 := file.HashSHA256
	if serverSHA256 == "" {
		if info, _, err := client.Files().Info(ctx, file.ID); err == nil {
			serverSHA256 = info.HashSHA256
		}
	}
	if serverSHA256 == "" {
		return false, nil
	}
	if !strings.EqualFold(serverSHA256, localSHA256) {
		return false, fmt.Errorf("file %d on the server doesn't match what was uploaded: its SHA256 is %s, but the local file's is %s", file.ID, serverSHA256, localSHA256)
	}
	return true, nil
}
