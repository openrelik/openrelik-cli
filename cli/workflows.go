package cli

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/openrelik/openrelik-cli/util"
	"github.com/spf13/cobra"
)

func newWorkflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workflow",
		Aliases: []string{"workflows"},
		Short:   "Manage workflows",
		Long:    `Create, inspect, and run OpenRelik workflows.`,
	}

	cmd.AddCommand(newWorkflowCreateCmd())
	cmd.AddCommand(newWorkflowInfoCmd())
	cmd.AddCommand(newWorkflowStatusCmd())
	cmd.AddCommand(newWorkflowRunCmd())
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
					return fmt.Errorf("invalid JSON for --params: %w", err)
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

	cmd.Flags().IntVarP(&folderID, "folder", "f", 0, "Folder ID (optional, resolved from first file if omitted)")
	cmd.Flags().IntSliceVarP(&fileIDs, "file", "i", nil, "File IDs to include (can be specified multiple times)")
	cmd.Flags().IntVarP(&templateID, "template", "t", 0, "Template ID to use")
	cmd.Flags().StringVarP(&params, "params", "p", "", "JSON string of parameters")
	cmd.Flags().BoolVarP(&runAfter, "run", "r", false, "Run the workflow immediately after creation")

	_ = cmd.MarkFlagRequired("template")

	return cmd
}

func newWorkflowInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <WORKFLOW_ID>",
		Short: "Get workflow metadata",
		Long: `Display metadata for a workflow, including its files, folder, and spec.

WORKFLOW_ID is the integer ID of the workflow.`,
		Example: `  # Show workflow metadata
  openrelik workflow info 99

  # Output as JSON
  openrelik workflow info 99 --format json`,
		Args:  util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid workflow ID: %w", err)
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
		Long: `Display the current execution status of a workflow and its tasks.

WORKFLOW_ID is the integer ID of the workflow.`,
		Example: `  # Check workflow status
  openrelik workflow status 99

  # Poll status in a shell loop
  watch -n 5 openrelik workflow status 99`,
		Args:  util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid workflow ID: %w", err)
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

			status, _, err := client.Workflows().Status(cmd.Context(), workflow.Folder.ID, wID)
			if err != nil {
				return err
			}

			// The status endpoint omits runtime; patch it in from the full workflow.
			runtimeByID := make(map[int]*float64, len(workflow.Tasks))
			for _, t := range workflow.Tasks {
				if t.Runtime != nil {
					runtimeByID[t.ID] = t.Runtime
				}
			}
			for i := range status.Tasks {
				if rt, ok := runtimeByID[status.Tasks[i].ID]; ok {
					status.Tasks[i].Runtime = rt
				}
			}

			return formatAndPrint(cmd, &WorkflowStatusView{WorkflowStatus: status})
		},
	}
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
		Args:  util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			wID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid workflow ID: %w", err)
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

	cmd.Flags().StringVarP(&spec, "spec", "s", "", "JSON string of workflow specification")
	return cmd
}
