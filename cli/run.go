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
	"errors"
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
		Short: "Create and run workflows",
		Long: `Execute OpenRelik workers on files.

Subcommands are generated from the workers registered on the server. The
list of workers is cached for an hour, and updated when a workflow is
started, when this help is shown, or when a worker isn't known. Use
'openrelik worker list' to update it at any time.

Inputs are file IDs, local files, or paths of files on the server:

  openrelik run strings 123                    # file ID
  openrelik run strings report.pdf             # local file (uploaded first)
  openrelik run strings "Case A/disk.E01"      # file on the server

A local file is uploaded to the upload folder, unless the folder already
holds a file with the same name and SHA256, which is then used instead.
An input that isn't a local file and contains a '/' is a path on the
server, as in 'file info'. If a relative path names both a local file and
a file on the server, run fails rather than guess: write ./PATH for the
local file, or use the file ID.

Chain workers in sequence with --then (each worker receives the outputs
of the previous one):

  openrelik run strings --then grep --regex "password" 123

Run workers in parallel with --and (both receive the same input files):

  openrelik run strings --and grep --regex "password" 123

When the workflow finishes, the output files of the final tasks are
downloaded: the end of a chain, or every worker run with --and. Use
--download all for the output files of every task, or --download none.
After a worker, --download-result or --no-download-result overrides this
for that worker's task:

  openrelik run strings --download-result --then grep --regex "password" 123

Use --dry-run to preview the generated workflow spec without executing it.
A dry run doesn't contact the server, and uses the cached list of workers.`,
		TraverseChildren: true,
	}

	// Global run flags
	runCmd.PersistentFlags().StringP("output-dir", "o", ".", "Output directory for downloads")
	runCmd.PersistentFlags().String("download", "final", "Results to download: final (the final tasks' output), all, or none")
	// Removed in favour of --download none; kept hidden to say so.
	runCmd.PersistentFlags().Bool("no-download", false, "")
	runCmd.PersistentFlags().MarkHidden("no-download")
	runCmd.PersistentFlags().Bool("task-folders", false, "Organize downloads into task folders")
	runCmd.PersistentFlags().Int("upload-folder-id", 0, "Folder ID to upload local files to")
	runCmd.PersistentFlags().String("upload-folder-name", "", "Folder name to upload local files to (default: CLI Uploads (username))")
	runCmd.PersistentFlags().String("then", "", "Chain workers (use as delimiter)")
	runCmd.PersistentFlags().String("and", "", "Run workers in parallel (use as delimiter)")

	// dry-run applies to all worker subcommands
	runCmd.PersistentFlags().Bool("dry-run", false, "Generate and display workflow spec without executing")

	// The worker subcommands come from the cache, whatever its age, so
	// building the command tree never waits for a server. The cache is
	// refreshed when it matters: when a workflow is started, when 'run'
	// shows its help, and when a worker isn't known.
	workers, _ := config.LoadCachedWorkers()
	addWorkerCmds(runCmd, workers)

	// Reached when no worker subcommand matches.
	runCmd.Args = cobra.ArbitraryArgs
	runCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		name := args[0]
		workers, err := fetchWorkers(cmd.Context())
		if err != nil {
			return fmt.Errorf("unknown worker %q, and the list of workers could not be updated: %w", name, err)
		}
		addWorkerCmds(cmd, workers)
		if util.FindWorker(name, workers) != nil {
			return fmt.Errorf("%q was registered since the list of workers was last updated; run the command again", name)
		}
		return usageErrorf("unknown worker %q (see 'openrelik worker list')", name)
	}

	defaultHelp := runCmd.HelpFunc()
	runCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		// Worker subcommands show their own help from the cache.
		if cmd == runCmd {
			// Don't keep the help waiting long for a slow server.
			parent := cmd.Context()
			if parent == nil {
				parent = context.Background()
			}
			ctx, cancel := context.WithTimeout(parent, helpRefreshTimeout)
			defer cancel()
			if workers, err := loadWorkers(ctx); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: the list of workers could not be updated, so it may be out of date or empty: %v\n\n", err)
			} else {
				addWorkerCmds(cmd, workers)
			}
		}
		defaultHelp(cmd, args)
	})

	return runCmd
}

// helpRefreshTimeout limits how long 'run --help' waits for the server to
// list its workers.
const helpRefreshTimeout = 5 * time.Second

// checkRemovedRunFlags fails if a removed flag of 'run' is used, either
// before the worker (parsed by 'run') or among the worker's own arguments.
// serverPathResolver resolves the inputs of 'run' that are paths of files
// on the server.
func serverPathResolver(client *openrelik.Client) util.ServerPathResolver {
	return func(ctx context.Context, path string) (int, error) {
		id, err := resolveFile(ctx, client, path)
		var notFound notFoundError
		if errors.As(err, &notFound) {
			return 0, &util.NotOnServerError{Err: err}
		}
		return id, err
	}
}

func checkRemovedRunFlags(cmd *cobra.Command, args []string) error {
	used := cmd.Flags().Changed("no-download")
	for _, arg := range args {
		if arg == "--no-download" || strings.HasPrefix(arg, "--no-download=") {
			used = true
		}
	}
	if used {
		return usageErrorf("'run --no-download' was removed. Use instead:\n\n  --download none")
	}
	return nil
}

// addWorkerCmds replaces the worker subcommands of runCmd.
func addWorkerCmds(runCmd *cobra.Command, workers []openrelik.Worker) {
	runCmd.RemoveCommand(runCmd.Commands()...)
	for _, worker := range workers {
		runCmd.AddCommand(createWorkerCmd(worker, workers))
	}
}

// fetchWorkers gets the workers registered on the server in use, and caches
// them.
func fetchWorkers(ctx context.Context) ([]openrelik.Worker, error) {
	client, err := newClient()
	if err != nil {
		return nil, err
	}
	workers, _, err := client.Workers().Registered(ctx)
	if err != nil {
		return nil, err
	}
	_ = config.SaveWorkersCache(resolveCredentials().ServerURL, workers) // best-effort
	return workers, nil
}

// loadWorkers returns the workers registered on the server in use, from the
// cache if it is recent and was saved for that server.
func loadWorkers(ctx context.Context) ([]openrelik.Worker, error) {
	return config.LoadOrRefreshWorkersCache(ctx, resolveCredentials().ServerURL, fetchWorkers)
}

func createWorkerCmd(worker openrelik.Worker, allWorkers []openrelik.Worker) *cobra.Command {
	cmd := &cobra.Command{
		Use:                util.WorkerCommand(worker) + " <INPUT>...",
		Short:              worker.DisplayName,
		Long:               worker.Description,
		DisableFlagParsing: true, // We will parse flags manually for each segment
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkRemovedRunFlags(cmd, args); err != nil {
				return err
			}

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
				if p := cmd.Parent(); p != nil {
					for _, sub := range p.Commands() {
						if sub.Name() == tempCmd.Name() {
							sub.DisableFlagParsing = false
							defer func() { sub.DisableFlagParsing = true }()
							return sub.Help()
						}
					}
					p.AddCommand(tempCmd)
					defer p.RemoveCommand(tempCmd)
				}
				tempCmd.SetOut(cmd.OutOrStdout())
				tempCmd.SetErr(cmd.ErrOrStderr())
				return tempCmd.Help()
			}

			// Root workflow type is always 'chain'
			spec, positionalArgs, taskDownloadPrefs, meta, err := util.BuildWorkflowSpec(cmd.Parent(), segments, delimiter, allWorkers, createWorkerCmd)
			if err != nil {
				return err
			}

			// Use resolved global flags from BuildWorkflowSpec
			downloadPolicy := meta.DownloadPolicy
			outputDir := meta.OutputDir
			taskFolders := meta.TaskFolders
			dryRun := meta.DryRun
			outputSpecified := outputFormat != "human"
			showProgress := !quiet && !outputSpecified

			if downloadPolicy != "all" && downloadPolicy != "final" && downloadPolicy != "none" {
				return usageErrorf("invalid download policy: %s (must be 'all', 'final', or 'none')", downloadPolicy)
			}

			// Check the inputs before anything is created or previewed.
			if err := util.CheckInputs(positionalArgs); err != nil {
				return err
			}

			if dryRun {
				if showProgress {
					fmt.Fprintln(cmd.ErrOrStderr(), "Dry run: generating workflow spec...")
					fmt.Fprintf(cmd.ErrOrStderr(), "Inputs: %v\n", positionalArgs)
				}
				if !quiet {
					encoder := json.NewEncoder(cmd.OutOrStdout())
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

			// The spec was built from the cache, which may be old or
			// from another server. Check it against the workers
			// registered now.
			if _, err := config.LoadWorkersCache(resolveCredentials().ServerURL); err != nil {
				workers, err := fetchWorkers(cmd.Context())
				if err != nil {
					return fmt.Errorf("could not get the registered workers: %w", err)
				}
				spec, positionalArgs, taskDownloadPrefs, meta, err = util.BuildWorkflowSpec(cmd.Parent(), segments, delimiter, workers, createWorkerCmd)
				if err != nil {
					return err
				}
			}

			fileIDs, totalUploaded, err := util.ResolveInputs(cmd.Context(), client, serverPathResolver(client), positionalArgs, showProgress, meta.UploadFolderID, meta.UploadFolderName)
			if err != nil {
				return err
			}

			if len(fileIDs) == 0 {
				return fmt.Errorf("no inputs provided (file IDs, local files or server paths)")
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
			workflowID := workflow.ID
			workflow, _, err = client.Workflows().Run(cmd.Context(), workflow.Folder.ID, workflow.ID, &specJSON)
			if err != nil {
				return fmt.Errorf("failed to start workflow %d: %w", workflowID, err)
			}
			// Show the ID before waiting, so it is known even if the
			// command is interrupted.
			if showProgress {
				fmt.Fprintf(cmd.ErrOrStderr(), "Workflow %d started in folder %d\n", workflow.ID, workflow.Folder.ID)
			}

			interactive := util.IsInteractiveTTY() && showProgress

			monitor := util.NewWorkflowMonitor(client, workflow, meta.TaskShortNames, meta.TaskUUIDs, meta.UUIDToShort, interactive, showProgress)
			var lastStatus *openrelik.WorkflowStatus
			lastStatus, err = monitor.Monitor(cmd.Context())
			if cmd.Context().Err() != nil {
				// Interrupted; stopping the CLI doesn't stop the workflow.
				return fmt.Errorf("interrupted; workflow %d keeps running on the server.\nCheck it with 'openrelik workflow status %d', and download its results with 'openrelik workflow download %d'", workflow.ID, workflow.ID, workflow.ID)
			}
			if err != nil && lastStatus == nil {
				return err
			}
			// A failed workflow may still have results from the tasks that
			// succeeded, so download them before reporting the failure.
			workflowErr := err

			// Phase 4: Download results
			var fullWorkflow *openrelik.Workflow
			var saved []*util.SavedFile
			if lastStatus != nil {
				saved, fullWorkflow, err = util.DownloadResults(cmd.Context(), client, workflow.ID, downloadPolicy, taskDownloadPrefs, outputDir, taskFolders, showProgress)
				if errors.Is(err, util.ErrFinalTasksUnknown) {
					err = fmt.Errorf("%w; get all results with 'openrelik workflow download %d --all'", err, workflow.ID)
				}
				if err != nil {
					return errors.Join(workflowErr, fmt.Errorf("results of workflow %d were not downloaded: %w", workflow.ID, err))
				}
			}

			if outputFormat != "human" && fullWorkflow != nil {
				if err := formatAndPrint(cmd, &WorkflowInfoView{Workflow: fullWorkflow}); err != nil {
					return err
				}
				return workflowErr
			}

			if err := formatAndPrint(cmd, &RunResultView{Workflow: workflow, Status: lastStatus.Status, Saved: saved}); err != nil {
				return err
			}
			var totalDownloaded int64
			for _, f := range saved {
				totalDownloaded += f.Size
			}
			monitor.PrintSummary(startTime, totalUploaded, totalDownloaded)

			return workflowErr
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
	cmd.Flags().Bool("download-result", false, "Download this task's output, whatever --download says")
	cmd.Flags().Bool("no-download-result", false, "Don't download this task's output, whatever --download says")

	return cmd
}
