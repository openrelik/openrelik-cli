package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
)

// --- Auth ---

// AuthStatusView renders the server and API key that commands use, where
// each comes from, and the user the key logs in as. The key itself is never
// shown.
type AuthStatusView struct {
	credentials
	User  *openrelik.User // nil if the login couldn't be checked
	Error string          // why the login check failed
}

func (v *AuthStatusView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	server, key := "not set", "not set"
	if v.ServerURL != "" {
		server = fmt.Sprintf("%s (from %s)", v.ServerURL, v.ServerSource)
	}
	if v.APIKey != "" {
		key = fmt.Sprintf("set (from %s)", v.APIKeySource)
	}
	fmt.Fprintf(tw, "Server\t%s\n", server)
	fmt.Fprintf(tw, "API key\t%s\n", key)
	if v.User != nil {
		fmt.Fprintf(tw, "User\t%s\n", v.User.Username)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// Make an override visible: the active server is what 'auth list' and
	// 'auth switch' show, but it isn't what commands talk to.
	if v.ActiveServer != "" && v.ActiveServer != v.ServerURL {
		fmt.Fprintf(w, "\nThe active server, %s, is not used while %s is set.\n", v.ActiveServer, v.ServerSource)
	}
	return nil
}

func (v *AuthStatusView) RenderVerbose(w io.Writer) error { return v.RenderHuman(w) }

func (v *AuthStatusView) UnwrapJSON() any {
	return struct {
		Server       string          `json:"server"`
		ServerSource string          `json:"server_source"`
		APIKeySet    bool            `json:"api_key_set"`
		APIKeySource string          `json:"api_key_source"`
		ActiveServer string          `json:"active_server"`
		User         *openrelik.User `json:"user"`
		Error        string          `json:"error,omitempty"`
	}{v.ServerURL, v.ServerSource, v.APIKey != "", v.APIKeySource, v.ActiveServer, v.User, v.Error}
}

// savedServer is a server saved with 'auth login'.
type savedServer struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	Active      bool   `json:"active"`
	APIKeySaved bool   `json:"api_key_saved"`
}

// AuthListView renders the saved servers, and points out when an
// environment variable or flag makes commands use another server.
type AuthListView struct {
	credentials
	Servers []savedServer
}

func (v *AuthListView) RenderHuman(w io.Writer) error {
	if len(v.Servers) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "#\tSERVER\tACTIVE\tAPI KEY")
		for _, s := range v.Servers {
			active, key := "", "not saved"
			if s.Active {
				active = "yes"
			}
			if s.APIKeySaved {
				key = "saved"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", s.Number, s.URL, active, key)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if v.ServerURL != "" && v.ServerURL != v.ActiveServer {
		if len(v.Servers) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "Commands use %s instead while %s is set.\n", v.ServerURL, v.ServerSource)
	}
	return nil
}

func (v *AuthListView) RenderVerbose(w io.Writer) error { return v.RenderHuman(w) }

func (v *AuthListView) UnwrapJSON() any {
	if v.Servers == nil {
		return []savedServer{}
	}
	return v.Servers
}

// --- Files ---

// FileInfoView renders a single file's metadata for human consumption,
// including where it came from, every hash and exact timestamps.
type FileInfoView struct{ *openrelik.File }

func (v *FileInfoView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\t%d\n", v.ID)
	fmt.Fprintf(tw, "Name\t%s\n", v.DisplayName)
	if v.OriginalPath != nil && *v.OriginalPath != "" {
		fmt.Fprintf(tw, "Original path\t%s\n", *v.OriginalPath)
	}
	if v.Description != nil && *v.Description != "" {
		fmt.Fprintf(tw, "Description\t%s\n", *v.Description)
	}
	if v.Folder.ID != 0 {
		folder := strconv.Itoa(v.Folder.ID)
		if v.Folder.DisplayName != "" {
			folder += " (" + v.Folder.DisplayName + ")"
		}
		fmt.Fprintf(tw, "Folder\t%s\n", folder)
	}
	if v.User.Username != "" {
		fmt.Fprintf(tw, "Owner\t%s\n", v.User.Username)
	}
	fmt.Fprintf(tw, "Size\t%s\n", exactSize(v.Filesize))
	fmt.Fprintf(tw, "Type\t%s\n", v.DataType)
	fmt.Fprintf(tw, "MIME\t%s\n", v.MagicMime)
	if v.MagicText != "" {
		fmt.Fprintf(tw, "Magic\t%s\n", v.MagicText)
	}
	// Every hash is listed, so a missing one is visibly missing rather than
	// left out. The server doesn't hash very large files.
	ssdeep := ""
	if v.HashSSDeep != nil {
		ssdeep = *v.HashSSDeep
	}
	for _, h := range []struct{ name, value string }{
		{"MD5", v.HashMD5}, {"SHA1", v.HashSHA1}, {"SHA256", v.HashSHA256}, {"SSDeep", ssdeep},
	} {
		if h.value == "" {
			h.value = "not computed"
		}
		fmt.Fprintf(tw, "%s\t%s\n", h.name, h.value)
	}
	fmt.Fprintf(tw, "Created\t%s\n", util.FormatTimeWithAge(v.CreatedAt))
	fmt.Fprintf(tw, "Updated\t%s\n", util.FormatTimeWithAge(v.UpdatedAt))
	if v.IsDeleted != nil && *v.IsDeleted {
		deleted := "yes"
		if v.DeletedAt != nil {
			deleted += ", " + util.FormatTimeWithAge(*v.DeletedAt)
		}
		fmt.Fprintf(tw, "Deleted\t%s\n", deleted)
	}
	return tw.Flush()
}

// exactSize formats a file size for a single file's metadata: the size in
// readable units, followed by the exact number of bytes when the units
// round it, e.g. "11.1KB (11366 bytes)".
func exactSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%s (%d bytes)", util.FormatBytes(n), n)
}

func (v *FileInfoView) UnwrapJSON() any { return v.File }

// --- Folders ---

// FolderCreatedView renders a one-line confirmation after folder creation,
// or after finding an existing folder with 'folder create --exist-ok'.
type FolderCreatedView struct {
	*openrelik.Folder
	Created bool
}

func (v *FolderCreatedView) RenderHuman(w io.Writer) error {
	verb := "created"
	if !v.Created {
		verb = "already exists"
	}
	fmt.Fprintf(w, "Folder %q %s (ID %d)\n", v.DisplayName, verb, v.ID)
	return nil
}

func (v *FolderCreatedView) UnwrapJSON() any {
	return struct {
		*openrelik.Folder
		Created bool `json:"created"`
	}{v.Folder, v.Created}
}

// UploadView renders the result of 'upload': the root folder it
// created, if any, and one line per uploaded file.
type UploadView struct {
	Created     *openrelik.Folder
	FolderID    int
	Uploads     []uploadedFile
	Skipped     []skippedFile
	NotUploaded []notUploadedDir
}

func (v *UploadView) renderSkipped(w io.Writer) {
	for _, s := range v.Skipped {
		fmt.Fprintf(w, "Skipped %q: already in folder %d as file %d (%s)\n",
			filepath.Base(s.Path), s.FolderID, s.FileID, s.Reason)
	}
	for _, d := range v.NotUploaded {
		fmt.Fprintf(w, "Not uploaded %q: %s\n", d.Path, d.Reason)
	}
}

func (v *UploadView) RenderHuman(w io.Writer) error {
	if v.Created != nil {
		fmt.Fprintf(w, "Folder %q created (ID %d)\n", v.Created.DisplayName, v.Created.ID)
	}
	for _, u := range v.Uploads {
		fmt.Fprintf(w, "File %q uploaded to folder %d (ID %d)\n",
			u.File.DisplayName, u.FolderID, u.File.ID)
		fmt.Fprintf(w, "  SHA256 %s (%s)\n", u.SHA256, verifiedText(u.Verified))
	}
	v.renderSkipped(w)
	return nil
}

// verifiedText describes whether a transferred file's SHA256 matched the
// server's.
func verifiedText(verified bool) string {
	if verified {
		return "verified"
	}
	return "not verified: the server has no hash for this file yet"
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
	if len(v.Uploads) > 0 && len(v.Skipped)+len(v.NotUploaded) > 0 {
		fmt.Fprintln(w)
	}
	v.renderSkipped(w)
	return nil
}

func (v *UploadView) UnwrapJSON() any {
	uploads := v.Uploads
	if uploads == nil {
		uploads = []uploadedFile{}
	}
	skipped := v.Skipped
	if skipped == nil {
		skipped = []skippedFile{}
	}
	notUploaded := v.NotUploaded
	if notUploaded == nil {
		notUploaded = []notUploadedDir{}
	}
	return struct {
		FolderID    int               `json:"folder_id"`
		Created     *openrelik.Folder `json:"created_folder"`
		Uploads     []uploadedFile    `json:"uploads"`
		Skipped     []skippedFile     `json:"skipped"`
		NotUploaded []notUploadedDir  `json:"not_uploaded"`
	}{v.FolderID, v.Created, uploads, skipped, notUploaded}
}

// DownloadView renders the result of 'download': where the file was saved
// and its SHA256.
type DownloadView struct{ *util.SavedFile }

func (v *DownloadView) RenderHuman(w io.Writer) error {
	renderSaved(w, v.SavedFile)
	return nil
}

// renderSaved prints where a downloaded file was saved and its SHA256.
func renderSaved(w io.Writer, f *util.SavedFile) {
	fmt.Fprintf(w, "File %d saved to %q\n", f.FileID, f.Path)
	fmt.Fprintf(w, "  SHA256 %s (%s)\n", f.SHA256, verifiedText(f.Verified))
}

func (v *DownloadView) UnwrapJSON() any { return v.SavedFile }

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
		fmt.Fprintf(tw, "%d\t%s\t%s\n", f.ID, f.Path, util.FormatTime(f.UpdatedAt))
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
	return (&folderRowWriter{w: w}).write(v.Entries)
}

// folderRowWriter writes the rows of a folder listing, possibly in batches as
// they arrive. The display name, the only column without a bounded width,
// comes last, so rows of later batches line up with earlier ones. The other
// columns are as wide as the widest value so far.
type folderRowWriter struct {
	w io.Writer
	// minIDWidth is the narrowest the ID column gets.
	minIDWidth int
	widths     [4]int
	headerDone bool
}

func (rw *folderRowWriter) write(entries []listEntry) error {
	// Like 'ls', an empty folder prints nothing.
	if len(entries) == 0 {
		return nil
	}
	rows := make([][5]string, 0, len(entries)+1)
	if !rw.headerDone {
		rw.headerDone = true
		rw.widths[0] = rw.minIDWidth
		rows = append(rows, [5]string{"ID", "UPDATED", "SIZE", "TYPE", "DISPLAY NAME"})
	}
	for _, e := range entries {
		if f := e.folder; f != nil {
			rows = append(rows, [5]string{strconv.Itoa(f.ID), util.FormatTime(f.UpdatedAt), "-", "folder", f.DisplayName + "/"})
			continue
		}
		f := e.file
		rows = append(rows, [5]string{strconv.Itoa(f.ID), util.FormatTime(f.UpdatedAt),
			util.FormatBytes(f.Filesize), f.DataType, f.DisplayName})
	}
	for _, row := range rows {
		for i := range rw.widths {
			rw.widths[i] = max(rw.widths[i], len(row[i]))
		}
	}
	var b strings.Builder
	for _, row := range rows {
		for i, width := range rw.widths {
			fmt.Fprintf(&b, "%-*s  ", width, row[i])
		}
		b.WriteString(row[4])
		b.WriteByte('\n')
	}
	_, err := io.WriteString(rw.w, b.String())
	return err
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

// SharedFoldersView renders the folders others shared with the user, for
// 'folder list --shared'. It is a folder listing with the owner of each
// folder, and the same JSON shape.
type SharedFoldersView struct{ FolderContentsView }

func (v *SharedFoldersView) RenderHuman(w io.Writer) error {
	if len(v.Entries) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDISPLAY NAME\tOWNER\tUPDATED")
	for _, e := range v.Entries {
		f := e.folder
		fmt.Fprintf(tw, "%d\t%s/\t%s\t%s\n", f.ID, f.DisplayName, f.User.Username, util.FormatTime(f.UpdatedAt))
	}
	return tw.Flush()
}

func (v *SharedFoldersView) RenderVerbose(w io.Writer) error {
	util.FprintTable(w, v.contents().Folders)
	return nil
}

// ShareResultView renders what 'folder share' or 'folder unshare' did for
// each user and group. Failures aren't listed in human output, since the
// error lists them.
type ShareResultView struct {
	Folder  *shareFolder
	Verb    string // "share" or "unshare"
	Results []shareResult
}

func (v *ShareResultView) RenderHuman(w io.Writer) error {
	var shown []shareResult
	for _, r := range v.Results {
		if r.Status != "failed" && r.Status != "not_shared" {
			shown = append(shown, r)
		}
	}
	if len(shown) == 0 {
		return nil
	}
	fmt.Fprintf(w, "Folder %d %q %sd:\n", v.Folder.ID, v.Folder.DisplayName, v.Verb)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range shown {
		previous := make([]string, len(r.PreviousRoles))
		for i, p := range r.PreviousRoles {
			previous[i] = roleName(p)
		}
		var what string
		switch r.Status {
		case "removed":
			what = "removed " + strings.Join(previous, ", ")
		case "changed":
			what = roleName(r.Role) + " (was " + strings.Join(previous, ", ") + ")"
		case "unchanged":
			what = roleName(r.Role) + " (unchanged)"
		default:
			what = roleName(r.Role)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", r.Type, r.Name, what)
	}
	return tw.Flush()
}

func (v *ShareResultView) RenderVerbose(w io.Writer) error { return v.RenderHuman(w) }

// shareFolderJSON identifies the folder in sharing output.
type shareFolderJSON struct {
	ID          int    `json:"id"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
}

func (f *shareFolder) json() shareFolderJSON {
	return shareFolderJSON{ID: f.ID, DisplayName: f.DisplayName, Path: f.path}
}

func (v *ShareResultView) UnwrapJSON() any {
	return struct {
		Folder  shareFolderJSON `json:"folder"`
		Results []shareResult   `json:"results"`
	}{v.Folder.json(), v.Results}
}

// FolderAccessView renders your role on a folder and the roles on it and
// its parents, for 'folder access'.
type FolderAccessView struct {
	Folder *shareFolder
	MyRole *openrelik.MyRole
	Grants []grant
}

// from says where a role comes from: the folder itself or a parent.
func (v *FolderAccessView) from(g grant) string {
	if g.folder.ID == v.Folder.ID {
		return "this folder"
	}
	return folderPath(g.folder)
}

// myRoleSource says how you got your role, if not directly on the folder:
// " (from /Case A/)", " (through group ir-team)" or both.
func (v *FolderAccessView) myRoleSource() string {
	var parts []string
	if id := v.MyRole.FolderID; id != nil && *id != v.Folder.ID {
		for f := v.Folder.Parent; f != nil; f = f.Parent {
			if f.ID == *id {
				parts = append(parts, "from "+folderPath(f))
			}
		}
	}
	if id := v.MyRole.GroupID; id != nil {
		name := fmt.Sprintf("group %d", *id)
		for _, g := range v.Grants {
			if g.kind == "group" && g.id == *id {
				name = "group " + g.name
			}
		}
		parts = append(parts, "through "+name)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (v *FolderAccessView) RenderHuman(w io.Writer) error { return v.render(w, false) }

// RenderVerbose adds the role IDs.
func (v *FolderAccessView) RenderVerbose(w io.Writer) error { return v.render(w, true) }

func (v *FolderAccessView) render(w io.Writer, verbose bool) error {
	fmt.Fprintf(w, "Folder %d %q (%s)\n", v.Folder.ID, v.Folder.DisplayName, v.Folder.path)
	fmt.Fprintf(w, "Your role: %s%s\n\n", roleName(v.MyRole.Role), v.myRoleSource())
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(tw, "TYPE\tNAME\tROLE\tFROM\tROLE ID")
	} else {
		fmt.Fprintln(tw, "TYPE\tNAME\tROLE\tFROM")
	}
	for _, g := range v.Grants {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s", g.kind, g.name, g.role, v.from(g))
		if verbose {
			fmt.Fprintf(tw, "\t%d", g.roleID)
		}
		fmt.Fprintln(tw)
	}
	return tw.Flush()
}

// accessJSON is a role in 'folder access' JSON output.
type accessJSON struct {
	RoleID      int    `json:"role_id"`
	Role        string `json:"role"`
	ID          int    `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	// InheritedFrom is the parent folder the role is set on; null for
	// roles on the folder itself.
	InheritedFrom *shareFolderJSON `json:"inherited_from"`
}

func (v *FolderAccessView) UnwrapJSON() any {
	users, groups := []accessJSON{}, []accessJSON{}
	for _, g := range v.Grants {
		a := accessJSON{RoleID: g.roleID, Role: roleJSON(g.role), ID: g.id, Name: g.name, DisplayName: g.displayName}
		if g.folder.ID != v.Folder.ID {
			a.InheritedFrom = &shareFolderJSON{ID: g.folder.ID, DisplayName: g.folder.DisplayName, Path: folderPath(g.folder)}
		}
		if g.kind == "user" {
			users = append(users, a)
		} else {
			groups = append(groups, a)
		}
	}
	return struct {
		Folder shareFolderJSON `json:"folder"`
		MyRole string          `json:"my_role"`
		// MyRoleFolderID and MyRoleGroupID say where your role comes from.
		MyRoleFolderID *int         `json:"my_role_folder_id"`
		MyRoleGroupID  *int         `json:"my_role_group_id"`
		Users          []accessJSON `json:"users"`
		Groups         []accessJSON `json:"groups"`
	}{v.Folder.json(), roleJSON(v.MyRole.Role), v.MyRole.FolderID, v.MyRole.GroupID, users, groups}
}

// FolderTreeView renders a folder tree like 'tree', with each entry's ID in
// brackets and a summary count at the end.
type FolderTreeView struct {
	Root        *treeNode
	FoldersOnly bool
	// ShowSize adds each file's size inside its brackets, like 'tree -s -h'.
	ShowSize bool
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
			details := strconv.Itoa(e.file.ID)
			if v.ShowSize {
				details += "  " + util.FormatBytes(e.file.Filesize)
			}
			fmt.Fprintf(w, "%s%s[%s]  %s\n", prefix, connector, details, e.file.DisplayName)
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
	if v.IsAdmin != nil {
		fmt.Fprintf(tw, "Admin\t%v\n", *v.IsAdmin)
	}
	return tw.Flush()
}

func (v *UserMeView) UnwrapJSON() any { return v.User }

// --- Workers ---

// WorkerListView renders registered workers as a table, with the 'run'
// subcommand that starts each one.
type WorkerListView struct{ Workers []openrelik.Worker }

func (v *WorkerListView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMMAND\tDISPLAY NAME\tDESCRIPTION")
	for _, wk := range v.Workers {
		desc := util.Truncate(wk.Description, 60)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", util.WorkerCommand(wk), wk.DisplayName, desc)
	}
	return tw.Flush()
}

// RenderVerbose adds the task and queue names, and doesn't shorten the
// descriptions.
func (v *WorkerListView) RenderVerbose(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMMAND\tDISPLAY NAME\tTASK NAME\tQUEUE\tDESCRIPTION")
	for _, wk := range v.Workers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", util.WorkerCommand(wk), wk.DisplayName, wk.TaskName, wk.QueueName, wk.Description)
	}
	return tw.Flush()
}

// workerJSON is a worker with the name of its 'run' subcommand.
type workerJSON struct {
	Command string `json:"command"`
	openrelik.Worker
}

func (v *WorkerListView) UnwrapJSON() any {
	out := make([]workerJSON, len(v.Workers))
	for i, wk := range v.Workers {
		out[i] = workerJSON{util.WorkerCommand(wk), wk}
	}
	return out
}

// --- Templates ---

// TemplateListView renders workflow templates as a table.
type TemplateListView struct{ Templates []openrelik.WorkflowTemplate }

func (v *TemplateListView) RenderHuman(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDISPLAY NAME\tDESCRIPTION")
	for _, t := range v.Templates {
		desc := util.Truncate(t.Description, 60)
		fmt.Fprintf(tw, "%d\t%s\t%s\n", t.ID, t.DisplayName, desc)
	}
	return tw.Flush()
}

func (v *TemplateListView) UnwrapJSON() any { return v.Templates }

// --- Workflows ---

// WorkflowStatusView renders workflow status with a per-task breakdown table.
type WorkflowStatusView struct{ *openrelik.WorkflowStatus }

func (v *WorkflowStatusView) RenderHuman(w io.Writer) error { return v.render(w, false) }

// RenderVerbose adds task IDs, UUIDs and timestamps to the task table.
func (v *WorkflowStatusView) RenderVerbose(w io.Writer) error { return v.render(w, true) }

func (v *WorkflowStatusView) render(w io.Writer, verbose bool) error {
	fmt.Fprintf(w, "Status: %s\n", v.Status)
	if len(v.Tasks) == 0 {
		return nil
	}
	fmt.Fprintln(w)
	return renderTasks(w, v.Tasks, verbose)
}

func (v *WorkflowStatusView) UnwrapJSON() any { return v.WorkflowStatus }

// renderTasks prints a table of tasks with their status, runtime and output
// files, one output file per line. verbose adds IDs, UUIDs and timestamps.
func renderTasks(w io.Writer, tasks []openrelik.Task, verbose bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(tw, "ID\tUUID\tTASK\tSTATUS\tRUNTIME\tCREATED\tUPDATED\tOUTPUT ID\tOUTPUT FILE")
	} else {
		fmt.Fprintln(tw, "TASK\tSTATUS\tRUNTIME\tOUTPUT ID\tOUTPUT FILE")
	}
	for _, t := range tasks {
		status := "PENDING"
		if t.StatusShort != nil {
			status = *t.StatusShort
		}
		runtime := "-"
		if t.Runtime != nil {
			runtime = fmt.Sprintf("%.1fs", *t.Runtime)
		}
		cols := []string{t.DisplayName, status, runtime}
		blank := []string{"", "", ""}
		if verbose {
			cols = append([]string{strconv.Itoa(t.ID), t.UUID}, append(cols, util.FormatTime(t.CreatedAt), util.FormatTime(t.UpdatedAt))...)
			blank = make([]string, len(cols))
		}
		if len(t.OutputFiles) == 0 {
			fmt.Fprintln(tw, strings.Join(append(cols, "-", "-"), "\t"))
			continue
		}
		for i, f := range t.OutputFiles {
			row := cols
			if i > 0 {
				row = blank
			}
			fmt.Fprintln(tw, strings.Join(append(row, strconv.Itoa(f.ID), f.DisplayName), "\t"))
		}
	}
	return tw.Flush()
}

// WorkflowInfoView renders workflow metadata: its folder, input files,
// tasks with their output files, and the settings each task ran with. The
// raw SpecJSON blob is only included in JSON output.
type WorkflowInfoView struct{ *openrelik.Workflow }

func (v *WorkflowInfoView) RenderHuman(w io.Writer) error { return v.render(w, false) }

// RenderVerbose adds UUIDs, the owner, exact timestamps and file details.
func (v *WorkflowInfoView) RenderVerbose(w io.Writer) error { return v.render(w, true) }

func (v *WorkflowInfoView) render(w io.Writer, verbose bool) error {
	when := util.FormatTimeWithAge
	if verbose {
		when = util.FormatTime
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID\t%d\n", v.ID)
	fmt.Fprintf(tw, "Name\t%s\n", v.DisplayName)
	if verbose {
		fmt.Fprintf(tw, "UUID\t%s\n", v.UUID)
		if v.Description != nil && *v.Description != "" {
			fmt.Fprintf(tw, "Description\t%s\n", *v.Description)
		}
		fmt.Fprintf(tw, "Owner\t%s\n", v.User.Username)
	}
	fmt.Fprintf(tw, "Folder\t%d\n", v.Folder.ID)
	if v.Template != nil {
		fmt.Fprintf(tw, "Template\t%s (ID %d)\n", v.Template.DisplayName, v.Template.ID)
	}
	fmt.Fprintf(tw, "Created\t%s\n", when(v.CreatedAt))
	fmt.Fprintf(tw, "Updated\t%s\n", when(v.UpdatedAt))
	if err := tw.Flush(); err != nil {
		return err
	}

	if len(v.Files) > 0 {
		fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		// The workflow's files have no sizes; 'file info' has the details.
		if verbose {
			fmt.Fprintln(tw, "INPUT ID\tINPUT FILE\tTYPE")
		} else {
			fmt.Fprintln(tw, "INPUT ID\tINPUT FILE")
		}
		for _, f := range v.Files {
			if verbose {
				fmt.Fprintf(tw, "%d\t%s\t%s\n", f.ID, f.DisplayName, f.DataType)
			} else {
				fmt.Fprintf(tw, "%d\t%s\n", f.ID, f.DisplayName)
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	tasks := util.FlattenTasks(v.Tasks)
	if len(tasks) == 0 {
		return nil
	}
	fmt.Fprintln(w)
	if err := renderTasks(w, tasks, verbose); err != nil {
		return err
	}

	settings := util.TaskSettings(v.SpecJSON)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := false
	for _, t := range tasks {
		cfg := settings[t.UUID]
		if len(cfg) == 0 {
			continue
		}
		if !header {
			fmt.Fprintln(tw, "\nTASK\tSETTINGS")
			header = true
		}
		values := make([]string, len(cfg))
		for i, c := range cfg {
			values[i] = c.Name + "=" + formatSetting(c.Value)
		}
		fmt.Fprintf(tw, "%s\t%s\n", t.DisplayName, strings.Join(values, " "))
	}
	return tw.Flush()
}

// formatSetting formats a task setting's value, quoting strings that are
// empty or contain spaces so the NAME=VALUE pairs stay readable.
func formatSetting(v any) string {
	switch v := v.(type) {
	case nil:
		return `""`
	case string:
		if v == "" || strings.ContainsAny(v, " \t\"") {
			return strconv.Quote(v)
		}
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (v *WorkflowInfoView) UnwrapJSON() any { return v.Workflow }

// WorkflowListView renders the workflows found by 'workflow list'.
type WorkflowListView struct{ Workflows []*openrelik.Workflow }

func (v *WorkflowListView) RenderHuman(w io.Writer) error { return v.render(w, false) }

// RenderVerbose adds UUIDs, the owner, input file IDs and exact timestamps.
func (v *WorkflowListView) RenderVerbose(w io.Writer) error { return v.render(w, true) }

func (v *WorkflowListView) render(w io.Writer, verbose bool) error {
	// Like 'ls', no workflows prints nothing.
	if len(v.Workflows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(tw, "ID\tUUID\tNAME\tFOLDER\tOWNER\tINPUT IDS\tTASKS\tCREATED\tUPDATED")
	} else {
		fmt.Fprintln(tw, "ID\tNAME\tFOLDER\tTASKS\tCREATED")
	}
	for _, wf := range v.Workflows {
		tasks := taskSummary(util.FlattenTasks(wf.Tasks))
		if verbose {
			ids := make([]string, len(wf.Files))
			for i, f := range wf.Files {
				ids[i] = strconv.Itoa(f.ID)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", wf.ID, wf.UUID, wf.DisplayName, wf.Folder.ID,
				wf.User.Username, strings.Join(ids, ","), tasks, util.FormatTime(wf.CreatedAt), util.FormatTime(wf.UpdatedAt))
		} else {
			fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n", wf.ID, wf.DisplayName, wf.Folder.ID, tasks, util.FormatTime(wf.CreatedAt))
		}
	}
	return tw.Flush()
}

func (v *WorkflowListView) UnwrapJSON() any {
	if v.Workflows == nil {
		return []*openrelik.Workflow{}
	}
	return v.Workflows
}

// taskSummary counts tasks by status, e.g. "2 SUCCESS, 1 FAILURE", in the
// order the statuses first appear. A workflow that never ran has no tasks.
func taskSummary(tasks []openrelik.Task) string {
	if len(tasks) == 0 {
		return "not run"
	}
	var order []string
	counts := map[string]int{}
	for _, t := range tasks {
		status := "PENDING"
		if t.StatusShort != nil {
			status = *t.StatusShort
		}
		if counts[status] == 0 {
			order = append(order, status)
		}
		counts[status]++
	}
	parts := make([]string, len(order))
	for i, s := range order {
		parts[i] = fmt.Sprintf("%d %s", counts[s], s)
	}
	return strings.Join(parts, ", ")
}

// WorkflowDownloadView renders the files saved by 'workflow download'.
type WorkflowDownloadView struct{ Saved []*util.SavedFile }

func (v *WorkflowDownloadView) RenderHuman(w io.Writer) error {
	for _, f := range v.Saved {
		renderSaved(w, f)
	}
	return nil
}

func (v *WorkflowDownloadView) UnwrapJSON() any {
	if v.Saved == nil {
		return []*util.SavedFile{}
	}
	return v.Saved
}

// RunResultView renders the outcome of 'run': the workflow, how it ended,
// and the result files that were saved.
type RunResultView struct {
	Workflow *openrelik.Workflow
	Status   string
	Saved    []*util.SavedFile
}

func (v *RunResultView) RenderHuman(w io.Writer) error {
	fmt.Fprintf(w, "Workflow %d finished: %s (folder %d)\n", v.Workflow.ID, v.Status, v.Workflow.Folder.ID)
	for _, f := range v.Saved {
		renderSaved(w, f)
	}
	return nil
}

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
