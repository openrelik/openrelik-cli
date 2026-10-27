package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
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

// --- Folders ---

// FolderCreatedView renders a one-line confirmation after folder creation.
type FolderCreatedView struct{ *openrelik.Folder }

func (v *FolderCreatedView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "Folder %q created (ID %d)\n", v.DisplayName, v.ID)
	return nil
}

func (v *FolderCreatedView) UnwrapJSON() any { return v.Folder }

// UploadView renders the result of 'folder upload': the root folder it
// created, if any, and one line per uploaded file.
type UploadView struct {
	Created  *openrelik.Folder
	FolderID int
	Uploads  []uploadedFile
}

func (v *UploadView) RenderHuman(w io.Writer) error {
	if v.Created != nil {
		fmt.Fprintf(w, "Folder %q created (ID %d)\n", v.Created.DisplayName, v.Created.ID)
	}
	for _, u := range v.Uploads {
		fmt.Fprintf(w, "File %q uploaded to folder %d (ID %d)\n",
			u.File.DisplayName, u.FolderID, u.File.ID)
	}
	return nil
}

func (v *UploadView) RenderVerbose(w io.Writer) error {
	if v.Created != nil {
		fmt.Fprintf(w, "Folder %q created (ID %d)\n\n", v.Created.DisplayName, v.Created.ID)
	}
	files := make([]openrelik.File, 0, len(v.Uploads))
	for _, u := range v.Uploads {
		files = append(files, *u.File)
	}
	util.FprintTable(w, files)
	return nil
}

func (v *UploadView) UnwrapJSON() any {
	uploads := v.Uploads
	if uploads == nil {
		uploads = []uploadedFile{}
	}
	return struct {
		FolderID int               `json:"folder_id"`
		Created  *openrelik.Folder `json:"created_folder"`
		Uploads  []uploadedFile    `json:"uploads"`
	}{v.FolderID, v.Created, uploads}
}

// searchFolder is a folder search result with its full path.
type searchFolder struct {
	openrelik.Folder
	Path string `json:"path"`
}

// FolderSearchView renders folder search results with their full paths.
type FolderSearchView struct{ Folders []searchFolder }

func (v *FolderSearchView) RenderHuman(w io.Writer) error {
	if len(v.Folders) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPATH\tUPDATED")
	for _, f := range v.Folders {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", f.ID, f.Path, util.FormatTimeAgo(f.UpdatedAt))
	}
	return tw.Flush()
}

// RenderVerbose shows the full folder fields, with the path in place of
// the display name.
func (v *FolderSearchView) RenderVerbose(w io.Writer) error {
	folders := make([]openrelik.Folder, len(v.Folders))
	for i, f := range v.Folders {
		folders[i] = f.Folder
		folders[i].DisplayName = f.Path
	}
	util.FprintTable(w, folders)
	return nil
}

func (v *FolderSearchView) UnwrapJSON() any {
	if v.Folders == nil {
		return []searchFolder{}
	}
	return v.Folders
}

// FolderContentsView renders the subfolders and files of a folder as one
// table. Folder names get a trailing '/' and TYPE "folder" so they can't be
// mistaken for files, since folder and file IDs are separate.
type FolderContentsView struct{ Entries []listEntry }

// folderContents is the JSON shape of a folder listing.
type folderContents struct {
	Folders []openrelik.Folder     `json:"folders"`
	Files   []openrelik.FolderFile `json:"files"`
}

func (v *FolderContentsView) RenderHuman(w io.Writer) error {
	// Like 'ls', an empty folder prints nothing.
	if len(v.Entries) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDISPLAY NAME\tSIZE\tTYPE\tUPDATED")
	for _, e := range v.Entries {
		if f := e.folder; f != nil {
			fmt.Fprintf(tw, "%d\t%s/\t-\tfolder\t%s\n",
				f.ID, f.DisplayName, util.FormatTimeAgo(f.UpdatedAt))
			continue
		}
		f := e.file
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			f.ID, f.DisplayName, util.FormatBytes(f.Filesize),
			f.DataType, util.FormatTimeAgo(f.UpdatedAt))
	}
	return tw.Flush()
}

func (v *FolderContentsView) RenderVerbose(w io.Writer) error {
	c := v.contents()
	fmt.Fprintln(w, "Folders:")
	util.FprintTable(w, c.Folders)
	fmt.Fprintln(w, "\nFiles:")
	util.FprintTable(w, c.Files)
	return nil
}

func (v *FolderContentsView) UnwrapJSON() any { return v.contents() }

// contents splits the entries into folders and files, keeping their order.
func (v *FolderContentsView) contents() folderContents {
	c := folderContents{Folders: []openrelik.Folder{}, Files: []openrelik.FolderFile{}}
	for _, e := range v.Entries {
		if e.folder != nil {
			c.Folders = append(c.Folders, *e.folder)
		} else {
			c.Files = append(c.Files, *e.file)
		}
	}
	return c
}

// FolderTreeView renders a folder tree like 'tree', with each entry's ID in
// brackets and a summary count at the end.
type FolderTreeView struct {
	Root        *treeNode
	FoldersOnly bool
}

func (v *FolderTreeView) RenderHuman(w io.Writer) error {
	if f := v.Root.folder; f != nil {
		fmt.Fprintf(w, "[%d]  %s/\n", f.ID, f.DisplayName)
	} else {
		fmt.Fprintln(w, ".")
	}
	var folders, files int
	var walk func(n *treeNode, prefix string)
	walk = func(n *treeNode, prefix string) {
		for i, e := range n.entries {
			connector, indent := "├── ", "│   "
			if i == len(n.entries)-1 {
				connector, indent = "└── ", "    "
			}
			if e.folder != nil {
				folders++
				fmt.Fprintf(w, "%s%s[%d]  %s/\n", prefix, connector, e.folder.ID, e.folder.DisplayName)
				walk(n.children[e.folder.ID], prefix+indent)
				continue
			}
			files++
			fmt.Fprintf(w, "%s%s[%d]  %s\n", prefix, connector, e.file.ID, e.file.DisplayName)
		}
	}
	walk(v.Root, "")

	summary := plural(folders, "folder", "folders")
	if !v.FoldersOnly {
		summary += ", " + plural(files, "file", "files")
	}
	fmt.Fprintf(w, "\n%s\n", summary)
	return nil
}

// RenderVerbose uses the tree drawing; a flat field listing would lose the
// structure.
func (v *FolderTreeView) RenderVerbose(w io.Writer) error { return v.RenderHuman(w) }

// folderTreeJSON is a folder with its contents nested in the same shape as
// 'folder list'. Folders and files are null when they were not fetched.
type folderTreeJSON struct {
	*openrelik.Folder
	Folders []folderTreeJSON       `json:"folders"`
	Files   []openrelik.FolderFile `json:"files"`
}

func (v *FolderTreeView) UnwrapJSON() any { return v.treeJSON(v.Root) }

func (v *FolderTreeView) treeJSON(n *treeNode) folderTreeJSON {
	out := folderTreeJSON{Folder: n.folder}
	if n.entries == nil {
		return out
	}
	out.Folders = []folderTreeJSON{}
	if !v.FoldersOnly {
		out.Files = []openrelik.FolderFile{}
	}
	for _, e := range n.entries {
		if e.folder != nil {
			out.Folders = append(out.Folders, v.treeJSON(n.children[e.folder.ID]))
		} else {
			out.Files = append(out.Files, *e.file)
		}
	}
	return out
}

// plural formats a count with the singular or plural noun.
func plural(n int, singular, pluralNoun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, pluralNoun)
}

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
