package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/openrelik/openrelik-cli/util"
)

// --- Files ---

// FileInfoView renders a single file's key fields for human consumption.
type FileInfoView struct{ *openrelik.File }

func (v *FileInfoView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\t%d\n", v.ID)
	fmt.Fprintf(tw, "Name\t%s\n", v.DisplayName)
	fmt.Fprintf(tw, "Size\t%s\n", util.FormatBytes(v.Filesize))
	fmt.Fprintf(tw, "Type\t%s\n", v.DataType)
	fmt.Fprintf(tw, "MIME\t%s\n", v.MagicMime)
	if v.HashSHA256 != "" {
		fmt.Fprintf(tw, "SHA256\t%s\n", v.HashSHA256)
	}
	fmt.Fprintf(tw, "Updated\t%s\n", util.FormatTimeAgo(v.UpdatedAt))
	return tw.Flush()
}

func (v *FileInfoView) UnwrapJSON() any { return v.File }

// FileListView renders a slice of FolderFiles as a table.
type FileListView struct{ Files []openrelik.FolderFile }

func (v *FileListView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDISPLAY NAME\tSIZE\tTYPE\tUPDATED")
	for _, f := range v.Files {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			f.ID, f.DisplayName, util.FormatBytes(f.Filesize),
			f.DataType, util.FormatTimeAgo(f.UpdatedAt))
	}
	return tw.Flush()
}

func (v *FileListView) UnwrapJSON() any { return v.Files }

// FileUploadedView renders a one-line confirmation for a single upload.
type FileUploadedView struct {
	File     *openrelik.File
	FolderID int
}

func (v *FileUploadedView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "File %q uploaded to folder %d (ID %d)\n",
		v.File.DisplayName, v.FolderID, v.File.ID)
	return nil
}

func (v *FileUploadedView) UnwrapJSON() any { return v.File }

// FileUploadedMultiView renders one confirmation line per file for a batch upload.
type FileUploadedMultiView struct {
	Files    []*openrelik.File
	FolderID int
}

func (v *FileUploadedMultiView) RenderHuman(w io.Writer) error {
	for _, f := range v.Files {
		fmt.Fprintf(w, "File %q uploaded to folder %d (ID %d)\n",
			f.DisplayName, v.FolderID, f.ID)
	}
	return nil
}

func (v *FileUploadedMultiView) UnwrapJSON() any { return v.Files }

// --- Folders ---

// FolderCreatedView renders a one-line confirmation after folder creation.
type FolderCreatedView struct{ *openrelik.Folder }

func (v *FolderCreatedView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "Folder %q created (ID %d)\n", v.DisplayName, v.ID)
	return nil
}

func (v *FolderCreatedView) UnwrapJSON() any { return v.Folder }

// FolderListView renders a slice of folders as a compact table.
type FolderListView struct{ Folders []openrelik.Folder }

func (v *FolderListView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDISPLAY NAME\tUPDATED")
	for _, f := range v.Folders {
		fmt.Fprintf(tw, "%d\t%s\t%s\n",
			f.ID, f.DisplayName, util.FormatTimeAgo(f.UpdatedAt))
	}
	return tw.Flush()
}

func (v *FolderListView) UnwrapJSON() any { return v.Folders }

// --- Users ---

// UserMeView renders the current user's profile fields.
type UserMeView struct{ *openrelik.User }

func (v *UserMeView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\t%d\n", v.ID)
	fmt.Fprintf(tw, "Username\t%s\n", v.Username)
	fmt.Fprintf(tw, "Display Name\t%s\n", v.DisplayName)
	if v.AuthMethod != "" {
		fmt.Fprintf(tw, "Auth Method\t%s\n", v.AuthMethod)
	}
	fmt.Fprintf(tw, "Admin\t%v\n", v.IsAdmin)
	return tw.Flush()
}

func (v *UserMeView) UnwrapJSON() any { return v.User }

// --- Workers ---

// WorkerListView renders registered workers as a table with the machine-usable task name.
type WorkerListView struct{ Workers []openrelik.Worker }

func (v *WorkerListView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DISPLAY NAME\tDESCRIPTION")
	for _, wk := range v.Workers {
		desc := wk.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\n", wk.DisplayName, desc)
	}
	return tw.Flush()
}

func (v *WorkerListView) UnwrapJSON() any { return v.Workers }

// --- Templates ---

// TemplateListView renders workflow templates as a table.
type TemplateListView struct{ Templates []openrelik.WorkflowTemplate }

func (v *TemplateListView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDISPLAY NAME\tDESCRIPTION")
	for _, t := range v.Templates {
		desc := t.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\n", t.ID, t.DisplayName, desc)
	}
	return tw.Flush()
}

func (v *TemplateListView) UnwrapJSON() any { return v.Templates }

// --- Workflows ---

// WorkflowStatusView renders workflow status with a per-task breakdown table.
type WorkflowStatusView struct{ *openrelik.WorkflowStatus }

func (v *WorkflowStatusView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "Status: %s\n", v.Status)
	if len(v.Tasks) == 0 {
		return nil
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tSTATUS\tRUNTIME")
	for _, t := range v.Tasks {
		status := ""
		if t.StatusShort != nil {
			status = *t.StatusShort
		}
		runtime := "—"
		if t.Runtime != nil {
			runtime = fmt.Sprintf("%.1fs", *t.Runtime)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.DisplayName, status, runtime)
	}
	return tw.Flush()
}

func (v *WorkflowStatusView) UnwrapJSON() any { return v.WorkflowStatus }

// WorkflowInfoView renders workflow metadata without the raw SpecJSON blob.
type WorkflowInfoView struct{ *openrelik.Workflow }

func (v *WorkflowInfoView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\t%d\n", v.ID)
	fmt.Fprintf(tw, "Name\t%s\n", v.DisplayName)
	if len(v.Files) > 0 {
		names := make([]string, len(v.Files))
		for i, f := range v.Files {
			names[i] = f.DisplayName
		}
		fmt.Fprintf(tw, "Files\t%s\n", strings.Join(names, ", "))
	}
	fmt.Fprintf(tw, "Updated\t%s\n", util.FormatTimeAgo(v.UpdatedAt))
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(v.Tasks) == 0 {
		return nil
	}
	fmt.Fprintln(w)
	tw2 := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw2, "TASK\tSTATUS")
	for _, t := range v.Tasks {
		status := ""
		if t.StatusShort != nil {
			status = *t.StatusShort
		}
		fmt.Fprintf(tw2, "%s\t%s\n", t.DisplayName, status)
	}
	return tw2.Flush()
}

func (v *WorkflowInfoView) UnwrapJSON() any { return v.Workflow }

// WorkflowCreatedView renders a one-line confirmation after workflow creation.
type WorkflowCreatedView struct{ *openrelik.Workflow }

func (v *WorkflowCreatedView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "Workflow %q created (ID %d)\n", v.DisplayName, v.ID)
	return nil
}

func (v *WorkflowCreatedView) UnwrapJSON() any { return v.Workflow }

// WorkflowStartedView renders a one-line confirmation after a workflow is triggered.
type WorkflowStartedView struct{ *openrelik.Workflow }

func (v *WorkflowStartedView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "Workflow %q started (ID %d)\n", v.DisplayName, v.ID)
	return nil
}

func (v *WorkflowStartedView) UnwrapJSON() any { return v.Workflow }
