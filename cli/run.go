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

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openrelik/openrelik-cli/config"
	"github.com/openrelik/openrelik-cli/util"
	"github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run a worker on files",
		Long: `Execute OpenRelik workers on files.

Subcommands are dynamically generated from registered workers. Run
'openrelik worker list' to refresh the worker cache if new workers are
not yet visible.

Inputs are either integer file IDs or local file paths (auto-uploaded):

  openrelik run strings 123           # file ID
  openrelik run strings report.pdf    # local file (uploaded first)

Chain workers in sequence with --then (each worker receives the outputs
of the previous one):

  openrelik run strings --then grep --regex "password" 123

Run workers in parallel with --and (both receive the same input files):

  openrelik run strings --and grep --regex "password" 123

Use --dry-run to preview the generated workflow spec without executing it.`,
		TraverseChildren: true,
	}

	// Global run flags
	runCmd.PersistentFlags().StringP("output-dir", "o", ".", "Output directory for downloads")
	runCmd.PersistentFlags().String("download", "final", "Download policy (final, all, none)")
	runCmd.PersistentFlags().Bool("no-download", false, "Do not download any results")
	runCmd.PersistentFlags().Bool("task-folders", false, "Organize downloads into task folders")
	runCmd.PersistentFlags().Int("upload-folder-id", 0, "Folder ID to upload local files to")
	runCmd.PersistentFlags().String("upload-folder-name", "", "Folder name to upload local files to (default: CLI Uploads (username))")
	runCmd.PersistentFlags().String("then", "", "Chain workers (use as delimiter)")
	runCmd.PersistentFlags().String("and", "", "Run workers in parallel (use as delimiter)")

	// dry-run applies to all worker subcommands
	runCmd.PersistentFlags().Bool("dry-run", false, "Generate and display workflow spec without executing")

	// Load workers from cache (auto-refreshing if missing or stale) to build dynamic subcommands.
	workers, err := config.LoadOrRefreshWorkersCache(context.Background(), func(ctx context.Context) ([]openrelik.Worker, error) {
		client, err := newClient()
		if err != nil {
			return nil, err
		}
		ws, _, err := client.Workers().Registered(ctx)
		return ws, err
	})
	if err != nil {
		// If workers cannot be loaded or fetched, skip dynamic subcommands.
		return runCmd
	}

	for _, worker := range workers {
		workerCmd := createWorkerCmd(worker, workers)
		runCmd.AddCommand(workerCmd)
	}

	return runCmd
}

func createWorkerCmd(worker openrelik.Worker, allWorkers []openrelik.Worker) *cobra.Command {
	// Worker names are usually something like "openrelik-worker-strings.tasks.strings"
	// We want a shorter alias if possible, but for now we'll use the task name parts.
	use := worker.TaskName
	parts := strings.Split(worker.TaskName, ".")
	if len(parts) > 0 {
		use = parts[len(parts)-1]
	}

	cmd := &cobra.Command{
		Use:                use,
		Short:              worker.DisplayName,
		Long:               worker.Description,
		DisableFlagParsing: true, // We will parse flags manually for each segment
		RunE: func(cmd *cobra.Command, args []string) error {
			// Slice arguments by --then or --and
			segments, delimiter, err := util.SliceArgs(cmd.Name(), args, "--then", "--and")
			if err != nil {
				return err
			}

			// Check if help was requested in ANY segment.
			// We show help for the LAST segment that has it.
			var helpWorker *openrelik.Worker
			for i := len(segments) - 1; i >= 0; i-- {
				segment := segments[i]
				hasHelp := false
				for _, arg := range segment {
					if arg == "--help" || arg == "-h" {
						hasHelp = true
						break
					}
				}
				if hasHelp {
					helpWorker = util.FindWorker(segment[0], allWorkers)
					if helpWorker != nil {
						break
					}
				}
			}

			if helpWorker != nil {
				tempCmd := createWorkerCmd(*helpWorker, nil)
				tempCmd.DisableFlagParsing = false
				return tempCmd.Help()
			}

			outputDir, _ := cmd.Flags().GetString("output-dir")
			taskFolders, _ := cmd.Flags().GetBool("task-folders")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			var downloadPolicy string

			outputSpecified := outputFormat != "human"
			showProgress := !quiet && !outputSpecified

			// Root workflow type is always 'chain'
			spec, positionalArgs, taskDownloadPrefs, meta, err := util.BuildWorkflowSpec(cmd, segments, delimiter, allWorkers, createWorkerCmd)
			if err != nil {
				return err
			}

			// Use resolved global flags from BuildWorkflowSpec
			downloadPolicy = meta.DownloadPolicy
			outputDir = meta.OutputDir
			taskFolders = meta.TaskFolders

			if downloadPolicy != "all" && downloadPolicy != "final" && downloadPolicy != "none" {
				return fmt.Errorf("invalid download policy: %s (must be 'all', 'final', or 'none')", downloadPolicy)
			}

			if dryRun {
				if showProgress {
					fmt.Println("Dry run: generating workflow spec...")
					fmt.Printf("Inputs: %v\n", positionalArgs)
				}
				if !quiet {
					encoder := json.NewEncoder(os.Stdout)
					encoder.SetIndent("", "  ")
					if err := encoder.Encode(spec); err != nil {
						return fmt.Errorf("failed to encode workflow spec: %w", err)
					}
				}
				return nil
			}

			if !dryRun && downloadPolicy != "none" {
				if info, err := os.Stat(outputDir); err != nil || !info.IsDir() {
					return fmt.Errorf("output directory %q does not exist", outputDir)
				}
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			fileIDs, totalUploaded, err := util.ResolveInputs(cmd.Context(), client, positionalArgs, showProgress, meta.UploadFolderID, meta.UploadFolderName)
			if err != nil {
				return err
			}

			if len(fileIDs) == 0 {
				return fmt.Errorf("no inputs provided (IDs or file paths)")
			}

			startTime := time.Now()

			// Step 1: Create Workflow
			workflow, _, err := client.Workflows().Create(cmd.Context(), 0, fileIDs, nil, nil)
			if err != nil {
				return fmt.Errorf("failed to create workflow with %d inputs: %w", len(fileIDs), err)
			}

			// Step 2: Run Workflow
			specBytes, err := json.Marshal(spec)
			if err != nil {
				return fmt.Errorf("failed to marshal workflow spec: %w", err)
			}
			specJSON := string(specBytes)
			workflow, _, err = client.Workflows().Run(cmd.Context(), workflow.Folder.ID, workflow.ID, &specJSON)
			if err != nil {
				return fmt.Errorf("failed to start workflow %d: %w", workflow.ID, err)
			}

			interactive := util.IsInteractiveTTY() && showProgress

			monitor := util.NewWorkflowMonitor(client, workflow, meta.TaskShortNames, meta.TaskUUIDs, meta.UUIDToShort, interactive, showProgress)
			var lastStatus *openrelik.WorkflowStatus
			lastStatus, err = monitor.Monitor(cmd.Context())
			if err != nil && lastStatus == nil {
				return err
			}

			// Phase 4: Download results
			var fullWorkflow *openrelik.Workflow
			var totalDownloaded int64
			if lastStatus != nil {
				totalDownloaded, fullWorkflow, err = util.DownloadResults(cmd.Context(), client, workflow.ID, downloadPolicy, taskDownloadPrefs, outputDir, taskFolders, outputFormat, showProgress)
				if err != nil {
					return err
				}
			}

			if outputFormat != "human" && fullWorkflow != nil {
				return formatAndPrint(cmd, &WorkflowInfoView{Workflow: fullWorkflow})
			}

			monitor.PrintSummary(startTime, totalUploaded, totalDownloaded)

			return nil
		},
	}

	// Add worker-specific flags based on TaskConfig
	for _, cfg := range worker.TaskConfig {
		switch cfg.Type {
		case "string", "text", "textarea", "artifacts", "autocomplete", "select":
			var def string
			if cfg.Default != nil {
				def = fmt.Sprintf("%v", cfg.Default)
			}
			cmd.Flags().String(cfg.Name, def, cfg.Description)
		case "boolean", "checkbox":
			var def bool
			if b, ok := cfg.Default.(bool); ok {
				def = b
			}
			cmd.Flags().Bool(cfg.Name, def, cfg.Description)
		case "integer":
			var def int
			if i, ok := cfg.Default.(float64); ok { // JSON unmarshals numbers as float64
				def = int(i)
			}
			cmd.Flags().Int(cfg.Name, def, cfg.Description)
		}
	}

	// Every worker command also gets its own download override flags
	cmd.Flags().Bool("download-result", false, "Download results for this task")
	cmd.Flags().Bool("no-download-result", false, "Do not download results for this task")

	return cmd
}
