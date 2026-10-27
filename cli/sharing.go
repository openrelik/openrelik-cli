package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

// shareRoles are the roles 'folder share' accepts, lowest first.
var shareRoles = []string{openrelik.RoleViewer, openrelik.RoleEditor, openrelik.RoleOwner}

// parseRole maps a --role value, in any case, to the role the server expects.
func parseRole(s string) (string, error) {
	for _, r := range shareRoles {
		if strings.EqualFold(s, r) {
			return r, nil
		}
	}
	return "", usageErrorf("invalid role %q (must be viewer, editor or owner)", s)
}

// roleName returns a role from the server as it is shown, e.g. "Owner". The
// server sends the role's value ("Owner") in some responses and its enum
// name ("OWNER") in others.
func roleName(role string) string {
	key := strings.ReplaceAll(role, "_", " ")
	for _, r := range append(shareRoles, openrelik.RoleNoAccess) {
		if strings.EqualFold(key, r) {
			return r
		}
	}
	return role
}

// roleJSON returns a role as it appears in JSON output: lowercase, so it can
// be passed back to --role.
func roleJSON(role string) string { return strings.ToLower(roleName(role)) }

// roleRank orders roles by how much they allow; unknown roles rank lowest.
func roleRank(role string) int {
	return slices.Index(shareRoles, roleName(role))
}

// grant is a role a user or group has on a folder.
type grant struct {
	kind        string // "user" or "group"
	id          int    // of the user or group
	name        string // username or group name
	displayName string
	roleID      int
	role        string // as shown, e.g. "Owner"
	// folder is the folder the role is set on: the folder itself or, for
	// inherited roles, one of its parents.
	folder *openrelik.Folder
}

// folderGrants returns the roles set directly on f.
func folderGrants(f *openrelik.Folder) []grant {
	var grants []grant
	for _, r := range f.UserRoles {
		grants = append(grants, grant{kind: "user", id: r.User.ID, name: r.User.Username,
			displayName: r.User.DisplayName, roleID: r.ID, role: roleName(r.Role), folder: f})
	}
	for _, r := range f.GroupRoles {
		grants = append(grants, grant{kind: "group", id: r.Group.ID, name: r.Group.Name,
			roleID: r.ID, role: roleName(r.Role), folder: f})
	}
	return grants
}

// allGrants returns the roles on f and on each of its parents, which give
// access to f as well.
func allGrants(f *openrelik.Folder) []grant {
	var grants []grant
	for ; f != nil; f = f.Parent {
		grants = append(grants, folderGrants(f)...)
	}
	return grants
}

// principal is a user or group named on the command line.
type principal struct {
	kind string // "user" or "group"
	name string
}

func (p principal) String() string { return p.kind + " " + p.name }

func (p principal) matches(g grant) bool { return g.kind == p.kind && g.name == p.name }

// principalsFromFlags returns the users and groups from --user and --group,
// without duplicates.
func principalsFromFlags(users, groups []string) ([]principal, error) {
	var out []principal
	add := func(kind string, names []string) error {
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" {
				return usageErrorf("--%s must not be empty", kind)
			}
			p := principal{kind, n}
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
		return nil
	}
	if err := add("user", users); err != nil {
		return nil, err
	}
	if err := add("group", groups); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, usageErrorf("give at least one --user or --group")
	}
	return out, nil
}

// grantsOf returns the grants in grants that belong to p.
func grantsOf(grants []grant, p principal) []grant {
	var out []grant
	for _, g := range grants {
		if p.matches(g) {
			out = append(out, g)
		}
	}
	return out
}

func roleNames(grants []grant) []string {
	names := make([]string, len(grants))
	for i, g := range grants {
		names[i] = g.role
	}
	return names
}

// shareFolder is the folder that 'folder share', 'unshare' and 'access'
// work on, with the roles set on it and on its parents.
type shareFolder struct {
	*openrelik.Folder
	path string
}

func (f *shareFolder) describe() string {
	return fmt.Sprintf("folder %d %q", f.ID, f.DisplayName)
}

// loadShareFolder resolves a folder argument and fetches the folder with its
// roles. If arg turns out to be the path of a file, the error explains that
// files are shared through their folder. hint is the rest of the command
// line, for that explanation.
func loadShareFolder(cmd *cobra.Command, client *openrelik.Client, arg, hint string) (*shareFolder, error) {
	ctx := cmd.Context()
	id, err := resolveFolder(ctx, client, arg)
	if errors.As(err, new(notFoundError)) && !strings.HasSuffix(arg, "/") {
		if _, fileErr := resolveFile(ctx, client, arg); fileErr == nil {
			dir := arg[:strings.LastIndex(arg, "/")+1]
			return nil, fileShareError(arg, cmd.Name(), dir, hint)
		}
	}
	if err != nil {
		return nil, err
	}
	f, _, err := client.Folders().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &shareFolder{Folder: f, path: folderPath(f)}, nil
}

// fileShareError explains that a file can't be shared on its own, with the
// command that shares its folder instead.
func fileShareError(file, verb, folder, hint string) error {
	file = shellQuote(file)
	if folderIDPattern.MatchString(file) {
		file = "file " + file
	}
	switch verb {
	case "access":
		return usageErrorf("%s is a file. Access to a file comes from the folder it is in:\n\n  openrelik folder access %s",
			file, shellQuote(folder))
	case "unshare":
		return usageErrorf("%s is a file. Files can't be unshared on their own; they are shared with the\nfolder they are in. To unshare the folder (and everything in it):\n\n  openrelik folder unshare %s%s",
			file, shellQuote(folder), hint)
	default:
		return usageErrorf("%s is a file. Files can't be shared on their own; they are shared with the\nfolder they are in. To share the folder (and everything in it):\n\n  openrelik folder share %s%s",
			file, shellQuote(folder), hint)
	}
}

// shareFlagsHint rebuilds the --user, --group and --role flags of cmd, so a
// hint can repeat them.
func shareFlagsHint(cmd *cobra.Command) string {
	var b strings.Builder
	for _, name := range []string{"user", "group"} {
		if f := cmd.Flags().Lookup(name); f != nil {
			values, _ := cmd.Flags().GetStringArray(name)
			for _, v := range values {
				fmt.Fprintf(&b, " --%s %s", name, shellQuote(v))
			}
		}
	}
	if role, _ := cmd.Flags().GetString("role"); role != "" {
		fmt.Fprintf(&b, " --role %s", shellQuote(role))
	}
	return b.String()
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// shellQuote quotes s for a command shown to the user, if it needs quoting.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shareResult is what happened to one user or group.
type shareResult struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Role is the role given; empty for 'folder unshare'.
	Role string `json:"role,omitempty"`
	// PreviousRoles are the roles the user or group had on the folder
	// before, which were replaced or removed.
	PreviousRoles []string `json:"previous_roles"`
	// Status is one of added, changed, unchanged, removed, not_shared or
	// failed.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`

	err error // why it failed, for the exit code
}

// shareChecks are the checks that run before anything is changed.
type shareChecks struct {
	cmd     *cobra.Command
	client  *openrelik.Client
	folder  *shareFolder
	force   bool
	changes []principal
}

// check refuses changes that would leave the folder without an owner, and
// asks before you remove or lower your own role. after returns the role p
// will have on the folder ("" for none).
func (c *shareChecks) check(after func(p principal) string) error {
	grants := allGrants(c.folder.Folder)
	// Owner roles on parent folders and of others stay; those changed end
	// up as after says.
	owners, ownersAfter := 0, 0
	for _, g := range grants {
		if g.role != openrelik.RoleOwner {
			continue
		}
		owners++
		if g.folder.ID != c.folder.ID || !slices.Contains(c.changes, principal{g.kind, g.name}) {
			ownersAfter++
		}
	}
	for _, p := range c.changes {
		if after(p) == openrelik.RoleOwner {
			ownersAfter++
		}
	}
	if owners > 0 && ownersAfter == 0 {
		return fmt.Errorf("%s would have no owner; give someone else the Owner role first", c.folder.describe())
	}

	var mine principal
	for _, p := range c.changes {
		if p.kind == "user" {
			me, _, err := c.client.Users().GetMe(c.cmd.Context())
			if err != nil {
				return fmt.Errorf("failed to look up your user: %w", err)
			}
			mine = principal{"user", me.Username}
			break
		}
	}
	if !slices.Contains(c.changes, mine) {
		return nil
	}
	current := grantsOf(folderGrants(c.folder.Folder), mine)
	if len(current) == 0 {
		return nil
	}
	highest := slices.MaxFunc(current, func(a, b grant) int { return roleRank(a.role) - roleRank(b.role) })
	var question string
	switch role := after(mine); {
	case role == "":
		question = fmt.Sprintf("This removes your own %s role on %s; you may lose access to it. Continue?", highest.role, c.folder.describe())
	case roleRank(role) < roleRank(highest.role):
		question = fmt.Sprintf("This changes your own role on %s from %s to %s. Continue?", c.folder.describe(), highest.role, role)
	default:
		return nil
	}
	if c.force {
		return nil
	}
	if !stdinIsTerminal(c.cmd.InOrStdin()) {
		return fmt.Errorf("%s\nNo terminal to ask on; use --force to confirm.", question)
	}
	confirmed, err := util.Confirm(c.cmd.ErrOrStderr(), c.cmd.InOrStdin(), question)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("cancelled; access was not changed")
	}
	return nil
}

// describeFailure says in plain words why a request for p failed. A 404
// when sharing means the user or group doesn't exist, since the folder was
// just read; the general rewrite in explainError would blame the folder.
func describeFailure(cmd *cobra.Command, folderID int, p principal, err error) error {
	if debug {
		return err
	}
	var apiErr *openrelik.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusNotFound && apiErr.Response != nil && apiErr.Response.Request.Method == http.MethodPost:
			return &wrappedFailure{notFoundError(fmt.Sprintf("no %s named %q", p.kind, p.name)), err}
		case apiErr.StatusCode == http.StatusNotFound:
			// Removing a role: the path ends in the role's ID, which
			// explainError would take for a user or group ID.
			return &wrappedFailure{fmt.Errorf("the server didn't find the role to remove"), err}
		case apiErr.StatusCode == http.StatusForbidden:
			explained := explainError(err, false, "")
			return &wrappedFailure{fmt.Errorf("%v; sharing needs the Editor or Owner role; check yours with 'openrelik folder access %d'", explained, folderID), err}
		}
	}
	interrupted := cmd.Context() != nil && cmd.Context().Err() != nil
	return explainError(err, interrupted, apiKeyHint(cmd))
}

// wrappedFailure is an error with its own message that keeps cause in the
// chain, so the exit code still reflects it.
type wrappedFailure struct {
	msg   error
	cause error
}

func (e *wrappedFailure) Error() string { return e.msg.Error() }
func (e *wrappedFailure) Unwrap() []error {
	return []error{e.msg, e.cause}
}

// failed records err on r.
func (r *shareResult) failed(err error) {
	r.Status, r.Error, r.err = "failed", err.Error(), err
}

// sharingError reports the users and groups that failed. Its exit code is
// that of the first failure.
type sharingError struct {
	msg   string
	first error
}

func (e *sharingError) Error() string { return e.msg }
func (e *sharingError) Unwrap() error { return e.first }

func failures(results []shareResult, verb string) error {
	var failed []shareResult
	for _, r := range results {
		if r.Status == "failed" {
			failed = append(failed, r)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "not %s with %d of %d:", verb, len(failed), len(results))
	for _, r := range failed {
		fmt.Fprintf(&b, "\n  %s %s: %s", r.Type, r.Name, r.Error)
	}
	return &sharingError{msg: b.String(), first: failed[0].err}
}

func newShareFolderCmd() *cobra.Command {
	var users, groups []string
	var role string
	var force bool

	cmd := &cobra.Command{
		Use:   "share <FOLDER>",
		Short: "Share a folder with users or groups",
		Long: `Give users and groups a role on a folder. Sharing a folder gives access
to everything in it: its files, and its subfolders and their files.

FOLDER is a folder ID or a path of folder names, as accepted by
'folder list'. Files can't be shared on their own; share their folder.

--role is required and is one of:

  viewer   see the folder and download its files
  editor   also upload files, run workflows and share the folder
  owner    full control, including deleting the folder

A user or group that already has a role on the folder ends up with only
the new one: if they already have exactly that role nothing changes,
otherwise the new role is added and then the old one removed. Lowering
your own role asks first; use --force to skip the question. A change that
would leave the folder without an owner is refused.

Each user and group is shared separately, and every one is reported. If
any fail, the others are still shared and the command exits non-zero.`,
		Example: `  # Let alice view a case folder and everything in it
  openrelik folder share "Case A/" --user alice --role viewer

  # Give two analysts and the IR team edit access to folder 42
  openrelik folder share 42 --user bob --user carol --group ir-team --role editor

  # Check who was shared with in a script
  openrelik folder share 42 --user alice --role viewer --format json | jq '.results[]'`,
		Args: util.UseArgsWithQuoteHint("user", "group"),
		RunE: func(cmd *cobra.Command, args []string) error {
			newRole, err := parseRole(role)
			if err != nil {
				return err
			}
			targets, err := principalsFromFlags(users, groups)
			if err != nil {
				return err
			}

			client, err := newClient()
			if err != nil {
				return err
			}
			folder, err := loadShareFolder(cmd, client, args[0], shareFlagsHint(cmd))
			if err != nil {
				return err
			}

			checks := &shareChecks{cmd: cmd, client: client, folder: folder, force: force, changes: targets}
			if err := checks.check(func(principal) string { return newRole }); err != nil {
				return err
			}

			direct := folderGrants(folder.Folder)
			results := make([]shareResult, len(targets))
			for i, p := range targets {
				results[i] = shareWith(cmd, client, folder, p, grantsOf(direct, p), newRole)
			}

			view := &ShareResultView{Folder: folder, Verb: "share", Results: results}
			if err := formatAndPrint(cmd, view); err != nil {
				return err
			}
			return failures(results, "shared")
		},
	}

	cmd.Flags().StringArrayVar(&users, "user", nil, "Username to share with (repeatable)")
	cmd.Flags().StringArrayVar(&groups, "group", nil, "Group name to share with (repeatable)")
	cmd.Flags().StringVar(&role, "role", "", "Role to give: viewer, editor or owner (required)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Don't ask before lowering your own role")
	cmd.MarkFlagRequired("role")
	return cmd
}

// shareWith gives p the role newRole on folder, where p currently has the
// roles current, and removes those roles once the new one is in place.
func shareWith(cmd *cobra.Command, client *openrelik.Client, folder *shareFolder, p principal, current []grant, newRole string) shareResult {
	ctx := cmd.Context()
	r := shareResult{Type: p.kind, Name: p.name, Role: roleJSON(newRole), PreviousRoles: []string{}}
	var old []grant
	hasRole := false
	for _, g := range current {
		if g.role == newRole && !hasRole {
			hasRole = true
			continue
		}
		old = append(old, g)
	}
	for _, g := range old {
		r.PreviousRoles = append(r.PreviousRoles, roleJSON(g.role))
	}

	if !hasRole {
		req := &openrelik.FolderShareRequest{}
		if p.kind == "user" {
			req.UserNames, req.UserRole = []string{p.name}, newRole
		} else {
			req.GroupNames, req.GroupRole = []string{p.name}, newRole
		}
		_, _, err := client.Folders().Share(ctx, folder.ID, req)
		var apiErr *openrelik.Error
		switch {
		case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict:
			// The role was added since the folder was read.
			hasRole = true
		case err != nil:
			r.failed(describeFailure(cmd, folder.ID, p, err))
			return r
		}
	}

	for i, g := range old {
		if err := deleteGrant(ctx, client, folder.ID, g); err != nil {
			left := append([]string{newRole}, roleNames(old[i:])...)
			err = describeFailure(cmd, folder.ID, p, err)
			r.failed(&wrappedFailure{fmt.Errorf("%v; %s now has the roles %s; remove them with 'openrelik folder unshare %d --%s %s' and share again",
				err, p.name, strings.Join(left, ", "), folder.ID, p.kind, shellQuote(p.name)), err})
			return r
		}
	}

	switch {
	case len(old) > 0:
		r.Status = "changed"
	case hasRole:
		r.Status = "unchanged"
	default:
		r.Status = "added"
	}
	return r
}

func deleteGrant(ctx context.Context, client *openrelik.Client, folderID int, g grant) error {
	var err error
	if g.kind == "user" {
		_, err = client.Folders().DeleteUserRole(ctx, folderID, g.roleID)
	} else {
		_, err = client.Folders().DeleteGroupRole(ctx, folderID, g.roleID)
	}
	return err
}

func newUnshareFolderCmd() *cobra.Command {
	var users, groups []string
	var force bool

	cmd := &cobra.Command{
		Use:   "unshare <FOLDER>",
		Short: "Remove users' or groups' access to a folder",
		Long: `Remove every role the given users and groups have on a folder.

FOLDER is a folder ID or a path of folder names, as accepted by
'folder list'.

Only roles set on the folder itself are removed. A user or group may
still have access through a role on a parent folder, or a user through a
group; 'folder access' shows where each role comes from, and the command
warns about the ones that remain.

Removing your own role asks first; use --force to skip the question. A
change that would leave the folder without an owner is refused.`,
		Example: `  # Remove alice's access to folder 42
  openrelik folder unshare 42 --user alice

  # Remove a group without being asked (for scripts)
  openrelik folder unshare "Case A/" --group contractors --force`,
		Args: util.UseArgsWithQuoteHint("user", "group"),
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := principalsFromFlags(users, groups)
			if err != nil {
				return err
			}

			client, err := newClient()
			if err != nil {
				return err
			}
			folder, err := loadShareFolder(cmd, client, args[0], shareFlagsHint(cmd))
			if err != nil {
				return err
			}

			checks := &shareChecks{cmd: cmd, client: client, folder: folder, force: force, changes: targets}
			if err := checks.check(func(principal) string { return "" }); err != nil {
				return err
			}

			direct := folderGrants(folder.Folder)
			results := make([]shareResult, len(targets))
			for i, p := range targets {
				results[i] = unshareFrom(cmd, client, folder, p, grantsOf(direct, p))
			}

			view := &ShareResultView{Folder: folder, Verb: "unshare", Results: results}
			if err := formatAndPrint(cmd, view); err != nil {
				return err
			}
			if !quiet {
				warnRemainingAccess(cmd, folder, results)
			}
			return failures(results, "unshared")
		},
	}

	cmd.Flags().StringArrayVar(&users, "user", nil, "Username to remove (repeatable)")
	cmd.Flags().StringArrayVar(&groups, "group", nil, "Group name to remove (repeatable)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Don't ask before removing your own role")
	return cmd
}

// unshareFrom removes the roles current that p has on folder.
func unshareFrom(cmd *cobra.Command, client *openrelik.Client, folder *shareFolder, p principal, current []grant) shareResult {
	r := shareResult{Type: p.kind, Name: p.name, PreviousRoles: []string{}}
	if len(current) == 0 {
		r.Status = "not_shared"
		return r
	}
	for i, g := range current {
		if err := deleteGrant(cmd.Context(), client, folder.ID, g); err != nil {
			err = describeFailure(cmd, folder.ID, p, err)
			if i > 0 {
				err = &wrappedFailure{fmt.Errorf("%v; %s still has the roles %s", err, p.name, strings.Join(roleNames(current[i:]), ", ")), err}
			}
			r.failed(err)
			return r
		}
		r.PreviousRoles = append(r.PreviousRoles, roleJSON(g.role))
	}
	r.Status = "removed"
	return r
}

// warnRemainingAccess warns on stderr about users and groups that weren't
// shared directly, and about roles on parent folders that still give access.
func warnRemainingAccess(cmd *cobra.Command, folder *shareFolder, results []shareResult) {
	w := cmd.ErrOrStderr()
	for _, r := range results {
		p := principal{r.Type, r.Name}
		if r.Status == "not_shared" {
			fmt.Fprintf(w, "Warning: %s had no role on %s.\n", p, folder.describe())
		}
		if r.Status == "failed" {
			continue
		}
		for parent := folder.Parent; parent != nil; parent = parent.Parent {
			for _, g := range grantsOf(folderGrants(parent), p) {
				fmt.Fprintf(w, "Warning: %s still has the %s role through %s (folder %d).\n",
					p, g.role, folderPath(parent), parent.ID)
			}
		}
	}
}

func newFolderAccessCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "access <FOLDER>",
		Short: "Show who can access a folder",
		Long: `Show your role on a folder, and the users and groups that have a role on
it. Roles on parent folders also give access, so they are listed too,
with the folder they are set on under FROM.

FOLDER is a folder ID or a path of folder names, as accepted by
'folder list'.

Users can also have access through a group they are in; group members
aren't listed.`,
		Example: `  # Show who can access a folder, and your own role
  openrelik folder access "Case A/"

  # List the users who can edit folder 42
  openrelik folder access 42 --format json | jq -r '.users[] | select(.role=="editor") | .name'`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			folder, err := loadShareFolder(cmd, client, args[0], "")
			if err != nil {
				return err
			}
			myRole, _, err := client.Folders().GetMyRole(cmd.Context(), folder.ID)
			if err != nil {
				return fmt.Errorf("failed to look up your role: %w", err)
			}

			view := &FolderAccessView{Folder: folder, MyRole: myRole, Grants: allGrants(folder.Folder)}
			sortGrants(view.Grants, folder.ID)
			if !quiet && outputFormat == "human" {
				for _, n := range multipleRoles(folderGrants(folder.Folder)) {
					fmt.Fprintf(cmd.ErrOrStderr(), "Note: %s has %d roles on this folder; 'openrelik folder share %d --%s %s --role ROLE' leaves one.\n",
						n.p, n.count, folder.ID, n.p.kind, shellQuote(n.p.name))
				}
			}
			return formatAndPrint(cmd, view)
		},
	}
}

// sortGrants sorts grants by role (Owner first), then type, then name, with
// roles on the folder itself before inherited ones.
func sortGrants(grants []grant, folderID int) {
	sort.SliceStable(grants, func(i, j int) bool {
		a, b := grants[i], grants[j]
		if ra, rb := roleRank(a.role), roleRank(b.role); ra != rb {
			return ra > rb
		}
		if a.kind != b.kind {
			return a.kind == "user"
		}
		if a.name != b.name {
			return strings.ToLower(a.name) < strings.ToLower(b.name)
		}
		return a.folder.ID == folderID && b.folder.ID != folderID
	})
}

type roleCount struct {
	p     principal
	count int
}

// multipleRoles returns the users and groups with more than one of grants.
func multipleRoles(grants []grant) []roleCount {
	var counts []roleCount
	for _, g := range grants {
		p := principal{g.kind, g.name}
		i := slices.IndexFunc(counts, func(c roleCount) bool { return c.p == p })
		if i < 0 {
			counts = append(counts, roleCount{p, 1})
		} else {
			counts[i].count++
		}
	}
	return slices.DeleteFunc(counts, func(c roleCount) bool { return c.count < 2 })
}

// newFileShareCmds are hidden 'file share' and 'file unshare' commands that
// explain that files are shared through their folder.
func newFileShareCmds() []*cobra.Command {
	var cmds []*cobra.Command
	for _, verb := range []string{"share", "unshare"} {
		cmd := &cobra.Command{
			Use:    verb + " <FILE>",
			Short:  "Files are shared through their folder; use 'folder " + verb + "'",
			Long:   "Files can't be " + verb + "d on their own; they are shared with the folder they\nare in. Use 'openrelik folder " + verb + "' on the folder instead.",
			Hidden: true,
			Args:   util.UseArgs(),
			RunE: func(cmd *cobra.Command, args []string) error {
				hint := shareFlagsHint(cmd)
				client, err := newClient()
				if err != nil {
					return err
				}
				id, err := resolveFile(cmd.Context(), client, args[0])
				if err != nil {
					return err
				}
				file, _, err := client.Files().Info(cmd.Context(), id)
				if err != nil {
					return err
				}
				return fileShareError(args[0], verb, fmt.Sprint(file.Folder.ID), hint)
			},
		}
		cmd.Flags().StringArray("user", nil, "")
		cmd.Flags().StringArray("group", nil, "")
		cmd.Flags().String("role", "", "")
		cmd.Flags().BoolP("force", "f", false, "")
		cmds = append(cmds, cmd)
	}
	return cmds
}
