package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func newWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workflow",
		Aliases: []string{"workflows"},
		Short:   "Manage workflows",
		Long:    `Create, inspect, and run OpenRelik workflows.`,
	}

	cmd.AddCommand(newWorkflowCreateCmd())
	cmd.AddCommand(newWorkflowListCmd())
	cmd.AddCommand(newWorkflowInfoCmd())
	cmd.AddCommand(newWorkflowStatusCmd())
	cmd.AddCommand(newWorkflowWaitCmd())
	cmd.AddCommand(newWorkflowRunCmd())
	cmd.AddCommand(newWorkflowDownloadCmd())
	return cmd
}

func newWorkflowCreateCmd() *cobra.Command {
	var folderID int
	var fileIDs []int
	var templateID int
	var params string
	var runAfter bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new workflow",
		Long: `Create a new workflow from one or more file IDs.

At least one --file ID is required. A --template ID is required to define
the workflow structure. The folder is resolved automatically from the first
file if --folder is not specified. Use --run to execute the workflow
immediately after creation.`,
		Example: `  # Create a workflow from file 10 using template 5
  openrelik workflow create --file 10 --template 5

  # Create and run immediately
  openrelik workflow create --file 10 --template 5 --run

  # Create in a specific folder
  openrelik workflow create --file 10 --template 5 --folder 42`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(fileIDs) == 0 {
				return fmt.Errorf("at least one file ID is required (use --file)")
			}

			var parsedParams map[string]any
			if params != "" {
				if err := json.Unmarshal([]byte(params), &parsedParams); err != nil {
					return usageErrorf("invalid JSON for --params: %w", err)
				}
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			workflow, _, err := client.Workflows().Create(cmd.Context(), folderID, fileIDs, &templateID, parsedParams)
			if err != nil {
				return err
			}

			if runAfter {
				var specPtr *string
				if workflow.SpecJSON != nil {
					specPtr = workflow.SpecJSON
				}
				workflow, _, err = client.Workflows().Run(cmd.Context(), workflow.Folder.ID, workflow.ID, specPtr)
				if err != nil {
					return err
				}
				return formatAndPrint(cmd, &WorkflowStartedView{Workflow: workflow})
			}

			return formatAndPrint(cmd, &WorkflowCreatedView{Workflow: workflow})
		},
	}

	// No short flags: -f, -p and -r mean --force, --parent and --reverse
	// elsewhere.
	cmd.Flags().IntVar(&folderID, "folder", 0, "Folder ID (optional, resolved from first file if omitted)")
	cmd.Flags().IntSliceVar(&fileIDs, "file", nil, "File IDs to include (can be specified multiple times)")
	cmd.Flags().IntVar(&templateID, "template", 0, "Template ID to use")
	cmd.Flags().StringVar(&params, "params", "", "JSON string of parameters")
	cmd.Flags().BoolVar(&runAfter, "run", false, "Run the workflow immediately after creation")

	_ = cmd.MarkFlagRequired("template")

	return cmd
}

func newWorkflowInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <WORKFLOW_ID>",
		Short: "Get workflow metadata",
		Long: `Display metadata for a workflow: its folder, the input files, each task
with its status and output files, and the settings each task ran with.

WORKFLOW_ID is the integer ID of the workflow. Pass the output file IDs to
commands such as 'download' and 'file cat', or download all of them with
'workflow download'. Use --format verbose for UUIDs and exact timestamps,
and --format json for the full workflow, including its spec.`,
		Example: `  # Show workflow metadata
  openrelik workflow info 99

  # Output as JSON
  openrelik workflow info 99 --format json

  # Print the IDs of all output files
  openrelik workflow info 99 --format json | jq '.tasks[].output_files[].id'`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil {
				return usageErrorf("invalid workflow ID: %w", err)
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			workflow, _, err := client.Workflows().Get(cmd.Context(), wID)
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &WorkflowInfoView{Workflow: workflow})
		},
	}
}

func newWorkflowStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <WORKFLOW_ID>",
		Short: "Get workflow status",
		Long: `Display the current execution status of a workflow and its tasks, with
the output files each task has produced so far.

WORKFLOW_ID is the integer ID of the workflow.`,
		Example: `  # Check workflow status
  openrelik workflow status 99

  # Poll status in a shell loop
  watch -n 5 openrelik workflow status 99`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil {
				return usageErrorf("invalid workflow ID: %w", err)
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			// First, get the workflow to resolve the folder ID
			workflow, _, err := client.Workflows().Get(cmd.Context(), wID)
			if err != nil {
				return err
			}
			status, err := workflowStatus(cmd.Context(), client, workflow)
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &WorkflowStatusView{WorkflowStatus: status})
		},
	}
}

func newWorkflowWaitCmd() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "wait <WORKFLOW_ID>",
		Short: "Wait for a workflow to finish",
		Long: `Wait until a workflow has finished, then print its status as 'workflow
status' does, with the output files of each task.

The exit status is 0 if the workflow succeeded and 1 if it failed, so
scripts can wait for a workflow and then act on the outcome. Progress is
shown on stderr while waiting. A workflow that has already finished is
reported at once; one that hasn't been started is an error.

Use --timeout to stop waiting after a while; the command then fails, and
the workflow keeps running on the server. Stopping the command with Ctrl-C
doesn't stop the workflow either.`,
		Example: `  # Wait for workflow 99, then download its results if it succeeded
  openrelik workflow wait 99 && openrelik workflow download 99

  # Wait at most 30 minutes
  openrelik workflow wait 99 --timeout 30m

  # Only use the exit status
  openrelik workflow wait 99 -q || echo "workflow 99 failed"`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil || wID <= 0 {
				return usageErrorf("invalid workflow ID: %q", args[0])
			}
			if timeout < 0 {
				return usageErrorf("invalid timeout: %s (must be 0 or greater)", timeout)
			}

			client, err := newClient()
			if err != nil {
				return err
			}
			workflow, _, err := client.Workflows().Get(cmd.Context(), wID)
			if err != nil {
				return err
			}
			tasks := util.FlattenTasks(workflow.Tasks)
			if len(tasks) == 0 {
				return fmt.Errorf("workflow %d hasn't been started; start it with 'openrelik workflow run %d'", wID, wID)
			}

			var names, uuids []string
			for _, t := range tasks {
				names = append(names, strings.ToLower(t.DisplayName))
				uuids = append(uuids, t.UUID)
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			monitor := util.NewWorkflowMonitor(client, workflow, names, uuids, nil, util.IsInteractiveTTY() && !quiet, !quiet)
			_, waitErr := monitor.Monitor(ctx)
			switch {
			case cmd.Context().Err() != nil:
				return fmt.Errorf("interrupted; workflow %d keeps running on the server", wID)
			case ctx.Err() != nil:
				return fmt.Errorf("workflow %d is still running after %s; it keeps running on the server", wID, timeout)
			case waitErr != nil && !errors.As(waitErr, new(*util.WorkflowFailedError)):
				return waitErr
			}

			// Fetch the workflow again for the output files the tasks
			// produced while we waited.
			workflow, _, err = client.Workflows().Get(cmd.Context(), wID)
			if err != nil {
				return err
			}
			status, err := workflowStatus(cmd.Context(), client, workflow)
			if err != nil {
				return err
			}
			if err := formatAndPrint(cmd, &WorkflowStatusView{WorkflowStatus: status}); err != nil {
				return err
			}
			return waitErr
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Stop waiting after this long, e.g. 30m or 2h (0 waits until the workflow finishes)")
	return cmd
}

// workflowStatus gets the status of workflow and its tasks. The status
// endpoint omits runtime and output files, so they are patched in from the
// full workflow.
func workflowStatus(ctx context.Context, client *openrelik.Client, workflow *openrelik.Workflow) (*openrelik.WorkflowStatus, error) {
	status, _, err := client.Workflows().Status(ctx, workflow.Folder.ID, workflow.ID)
	if err != nil {
		return nil, err
	}
	taskByID := map[int]openrelik.Task{}
	for _, t := range util.FlattenTasks(workflow.Tasks) {
		taskByID[t.ID] = t
	}
	for i := range status.Tasks {
		if t, ok := taskByID[status.Tasks[i].ID]; ok {
			if t.Runtime != nil {
				status.Tasks[i].Runtime = t.Runtime
			}
			status.Tasks[i].OutputFiles = t.OutputFiles
		}
	}
	return status, nil
}

func newWorkflowRunCmd() *cobra.Command {
	var spec string

	cmd := &cobra.Command{
		Use:   "run <WORKFLOW_ID>",
		Short: "Run a workflow",
		Long: `Execute a previously created workflow.

WORKFLOW_ID is the integer ID of the workflow. The workflow's existing spec
is used unless --spec provides a replacement. Use 'workflow status' to
monitor progress after starting.`,
		Example: `  # Run workflow 99 with its existing spec
  openrelik workflow run 99

  # Run with a custom spec
  openrelik workflow run 99 --spec '{"type":"chain","tasks":[]}'`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil {
				return usageErrorf("invalid workflow ID: %w", err)
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			// First, get the workflow to resolve the folder ID and fetch existing spec
			workflow, _, err := client.Workflows().Get(cmd.Context(), wID)
			if err != nil {
				return err
			}

			var specPtr *string
			if spec != "" {
				specPtr = &spec
			} else if workflow.SpecJSON != nil {
				specPtr = workflow.SpecJSON
			}

			updatedWorkflow, _, err := client.Workflows().Run(cmd.Context(), workflow.Folder.ID, wID, specPtr)
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &WorkflowStartedView{Workflow: updatedWorkflow})
		},
	}

	cmd.Flags().StringVar(&spec, "spec", "", "JSON string of workflow specification")
	return cmd
}

func newWorkflowListCmd() *cobra.Command {
	var recursive bool

	cmd := &cobra.Command{
		Use:     "list <FOLDER>",
		Aliases: []string{"ls"},
		Short:   "List workflows in a folder",
		Long: `List the workflows in a folder, with the status of their tasks.

FOLDER is a folder ID or a path of folder names, as accepted by
'folder list'. OpenRelik keeps each workflow in a folder of its own, created
inside the folder that holds its input files, so the workflows of FOLDER and
of the folders directly inside it are listed. Use --recursive to include
all folders below FOLDER.

Workflows are sorted by creation time, most recent last. FOLDER is the
workflow's own folder, which holds its output files.`,
		Example: `  # List the workflows run on files in folder 42
  openrelik workflow list 42

  # Include workflows anywhere below a case folder
  openrelik workflow list "Case A/" --recursive

  # Print the IDs of the workflows
  openrelik workflow list 42 --format json | jq '.[].id'`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			folderID, err := resolveFolder(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}
			ids, err := findWorkflows(cmd.Context(), client, folderID, recursive)
			if err != nil {
				return err
			}

			workflows := make([]*openrelik.Workflow, len(ids))
			g, ctx := errgroup.WithContext(cmd.Context())
			g.SetLimit(treeFetchConcurrency)
			for i, id := range ids {
				g.Go(func() error {
					wf, _, err := client.Workflows().Get(ctx, id)
					if err != nil {
						return fmt.Errorf("failed to get workflow %d: %w", id, err)
					}
					workflows[i] = wf
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return err
			}
			sort.SliceStable(workflows, func(i, j int) bool {
				if !workflows[i].CreatedAt.Equal(workflows[j].CreatedAt) {
					return workflows[i].CreatedAt.Before(workflows[j].CreatedAt)
				}
				return workflows[i].ID < workflows[j].ID
			})
			return formatAndPrint(cmd, &WorkflowListView{Workflows: workflows})
		},
	}

	cmd.Flags().BoolVar(&recursive, "recursive", false, "Include workflows in all folders below FOLDER")
	return cmd
}

// findWorkflows returns the IDs of the workflows in folderID and the folders
// directly inside it, or in all folders below it if recursive is set.
func findWorkflows(ctx context.Context, client *openrelik.Client, folderID int, recursive bool) ([]int, error) {
	folder, _, err := client.Folders().Get(ctx, folderID)
	if err != nil {
		return nil, err
	}
	ids := workflowIDs(folder.Workflows)

	var mu sync.Mutex
	level := []int{folderID}
	for len(level) > 0 {
		var next []int
		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(treeFetchConcurrency)
		for _, id := range level {
			g.Go(func() error {
				subs, _, err := client.Folders().ListSubFolders(ctx, id)
				if err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				for _, sub := range subs {
					ids = append(ids, workflowIDs(sub.Workflows)...)
					next = append(next, sub.ID)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		if !recursive {
			break
		}
		level = next
	}
	return ids, nil
}

// workflowIDs returns the IDs of the workflows listed in a folder.
func workflowIDs(workflows []any) []int {
	var ids []int
	for _, w := range workflows {
		if m, ok := w.(map[string]any); ok {
			if id, ok := m["id"].(float64); ok {
				ids = append(ids, int(id))
			}
		}
	}
	return ids
}

func newWorkflowDownloadCmd() *cobra.Command {
	var all, taskFolders bool

	cmd := &cobra.Command{
		Use:   "download <WORKFLOW_ID> [DIRECTORY]",
		Short: "Download the output files of a workflow",
		Long: `Download the output files of a workflow, as 'run' does when it finishes.

By default, only the output files of the final tasks are downloaded: the
tasks with no tasks after them, such as the end of a chain or each of the
tasks run in parallel at the end. Use --all to download the output files of
every task. For a workflow that is
still running, only the files produced so far are downloaded.

Files are saved in DIRECTORY, or in the current directory if it is omitted;
the directory must exist. Use --task-folders to save each task's files in a
folder of their own. As with 'download', each file's SHA256 is checked
against the server's before it is saved, and the names never place a file
outside DIRECTORY. Existing files are never replaced: if a name is taken,
nothing is downloaded.`,
		Example: `  # Download the final results of workflow 99
  openrelik workflow download 99

  # Download the output files of every task into ./results/
  openrelik workflow download 99 ./results/ --all --task-folders`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil || wID <= 0 {
				return usageErrorf("invalid workflow ID: %q", args[0])
			}
			dir := "."
			if len(args) > 1 {
				dir = args[1]
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				return fmt.Errorf("directory %q does not exist", dir)
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			policy := "final"
			if all {
				policy = "all"
			}
			saved, _, err := util.DownloadResults(cmd.Context(), client, wID, policy, nil, dir, taskFolders, !quiet)
			if errors.Is(err, util.ErrFinalTasksUnknown) {
				return fmt.Errorf("%w; use --all to download the results of every task", err)
			}
			if err != nil {
				return err
			}
			if len(saved) == 0 && !quiet {
				fmt.Fprintf(cmd.ErrOrStderr(), "Workflow %d has no output files to download.\n", wID)
			}
			return formatAndPrint(cmd, &WorkflowDownloadView{Saved: saved})
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Download the output files of every task, not only the last one")
	cmd.Flags().BoolVar(&taskFolders, "task-folders", false, "Save each task's files in a folder of their own")
	return cmd
}
