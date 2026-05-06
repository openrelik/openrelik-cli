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
	"fmt"
	"io"
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

				uploadedFile, _, err := client.Files().Upload(ctx, folder.ID, filename, f, openrelik.WithUploadProgress(progress))
				if err != nil {
					tracker.Finish()
					return fmt.Errorf("failed to upload file %s: %w", path, err)
				}
				tracker.Finish()
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

	for _, task := range tasksToDownload {
		for _, outputFile := range task.OutputFiles {
			destDir := outputDir
			if taskFolders {
				folderName := fmt.Sprintf("%s_%d", strings.ReplaceAll(task.DisplayName, " ", "_"), task.ID)
				destDir = filepath.Join(outputDir, folderName)
				if err := os.MkdirAll(destDir, 0755); err != nil {
					return 0, nil, fmt.Errorf("failed to create task folder: %w", err)
				}
			}

			destPath := filepath.Join(destDir, outputFile.DisplayName)
			f, err := os.Create(destPath)
			if err != nil {
				return 0, nil, fmt.Errorf("failed to create file %s: %w", destPath, err)
			}

			body, _, err := client.Files().Download(ctx, outputFile.ID)
			if err != nil {
				f.Close()
				return 0, nil, fmt.Errorf("failed to download file %s: %w", outputFile.DisplayName, err)
			}

			action := "Download: " + outputFile.DisplayName
			var trackerWriter io.Writer = os.Stderr
			if !showProgress {
				trackerWriter = nil
			}
			progressReader := &ProgressReader{
				Reader:  body,
				Tracker: NewProgressTracker(trackerWriter, outputFile.Filesize, action),
			}
			n, err := io.Copy(f, progressReader)
			body.Close()
			f.Close()

			if err != nil {
				return 0, nil, fmt.Errorf("failed to save file %s: %w", outputFile.DisplayName, err)
			}
			totalDownloaded += n
		}
	}

	return totalDownloaded, fullWorkflow, nil
}
