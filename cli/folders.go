package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func newFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "folder",
		Aliases: []string{"folders"},
		Short:   "Manage folders",
		Long:    `Create, list, search, share, and show trees of folders in OpenRelik.`,
	}

	cmd.AddCommand(newListFoldersCmd())
	cmd.AddCommand(newCreateFolderCmd())
	cmd.AddCommand(newSearchFoldersCmd())
	cmd.AddCommand(newTreeFoldersCmd())
	cmd.AddCommand(newShareFolderCmd())
	cmd.AddCommand(newUnshareFolderCmd())
	cmd.AddCommand(newFolderAccessCmd())
	cmd.AddCommand(removedFolderCmds()...)
	return cmd
}

// folderPageSize is the number of folders requested per API call.
const folderPageSize = 100

func newListFoldersCmd() *cobra.Command {
	var opts listOptions
	var shared bool

	cmd := &cobra.Command{
		Use:     "list [FOLDER]",
		Aliases: []string{"ls"},
		Short:   "List folder contents",
		Long: `List folder contents in OpenRelik.

Without FOLDER, lists all root folders: your own and those other users
shared with you. With --shared, lists only the folders shared with you,
with their owners. With FOLDER, lists the subfolders and files in that
folder.

FOLDER is either a folder ID or a path of folder names starting at the
root folders, such as 'Case A/evidence/'. An argument made only of
digits is an ID; add a trailing '/' to treat it as a name instead
(e.g. '2024/'). Names may contain '/': 'foo/bar/123' also finds a
folder named "foo/bar/123". If more than one folder matches a path, the
matches are listed so you can pick one by ID.

Folder names end in '/' and have TYPE folder. Folder and file IDs are
separate, so pass folder IDs to 'folder list' and file IDs to commands
such as 'file info'.

Like 'ls -latr', entries are sorted by last update with the most
recently updated entry last, so the latest work ends up next to the
prompt. ('folder tree' sorts by name instead, like 'tree', since update
times would scatter the folder structure.) Use --sort name to sort by name
and -r to reverse the sort order. Use --limit N to show only the N most
recently updated entries, in the selected sort order.`,
		Example: `  # List all root folders, most recently updated last
  openrelik folder list

  # List the folders other users shared with you
  openrelik folder list --shared

  # List subfolders and files in folder 42
  openrelik folder list 42

  # List a folder by its path from the root folders
  openrelik folder list "Case A/evidence/"

  # Most recently updated first
  openrelik folder list 42 -r

  # Sort by name
  openrelik folder list 42 --sort name

  # Show the 10 most recently updated entries
  openrelik folder list 42 --limit 10

  # Print the IDs of the files in folder 42
  openrelik folder list 42 --format json | jq '.files[].id'`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if shared && len(args) > 0 {
				return usageErrorf("--shared lists root folders, so it can't be used with FOLDER")
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			var fID int
			if len(args) > 0 {
				fID, err = resolveFolder(cmd.Context(), client, args[0])
				if err != nil {
					return err
				}
			}

			var folders []openrelik.Folder
			var files []openrelik.FolderFile
			noun := "folders"
			switch {
			case fID != 0:
				folders, files, err = fetchFolderContents(cmd.Context(), client, fID)
				noun = "items"
			case shared:
				folders, _, err = client.Folders().ListSharedFolders(cmd.Context())
			default:
				folders, err = util.ListAllRootFolders(cmd.Context(), client)
			}

			if err != nil {
				return err
			}

			entries := make([]listEntry, 0, len(folders)+len(files))
			for i := range folders {
				entries = append(entries, listEntry{folder: &folders[i]})
			}
			for i := range files {
				entries = append(entries, listEntry{file: &files[i]})
			}

			total := len(entries)
			entries = limitAndSortEntries(entries, opts.limit, opts.sortBy, opts.reverse)

			var view any = &FolderContentsView{Entries: entries}
			if shared {
				view = &SharedFoldersView{FolderContentsView{Entries: entries}}
			}
			if err := formatAndPrint(cmd, view); err != nil {
				return err
			}
			printTruncationHint(cmd, len(entries), total, noun, "Omit --limit to see all.")
			return nil
		},
	}

	opts.addFlags(cmd, 0)
	cmd.Flags().BoolVar(&shared, "shared", false, "List only the root folders other users shared with you")
	return cmd
}

// listOptions holds the sorting and limit flags shared by 'folder list'
// and 'search'.
type listOptions struct {
	sortBy  string
	reverse bool
	limit   int
}

func (o *listOptions) addFlags(cmd *cobra.Command, defaultLimit int) {
	cmd.Flags().StringVar(&o.sortBy, "sort", "time", "Sort by: time (oldest first) or name")
	cmd.Flags().BoolVarP(&o.reverse, "reverse", "r", false, "Reverse the sort order")
	cmd.Flags().IntVar(&o.limit, "limit", defaultLimit, "Show only the N most recently updated entries (0 shows all)")
}

func (o *listOptions) validate() error {
	if o.limit < 0 {
		return usageErrorf("invalid limit: %d (must be 0 or greater)", o.limit)
	}
	if o.sortBy != "time" && o.sortBy != "name" {
		return usageErrorf("invalid sort: %q (must be time or name)", o.sortBy)
	}
	return nil
}

// folderIDPattern matches arguments that are folder IDs rather than paths.
var folderIDPattern = regexp.MustCompile(`^[0-9]+$`)

// resolveFolder turns a folder argument into a folder ID. Arguments made
// only of digits are IDs; anything else is a path of folder names starting
// at the root folders.
func resolveFolder(ctx context.Context, client *openrelik.Client, arg string) (int, error) {
	if folderIDPattern.MatchString(arg) {
		id, err := strconv.Atoi(arg)
		if err != nil || id == 0 {
			return 0, usageErrorf("invalid folder ID: %s", arg)
		}
		return id, nil
	}
	return resolveFolderPath(ctx, client, arg)
}

// folderMatch is a folder found while resolving a path, with the names of
// the folders leading to it and the number of path segments they used up.
// folded is set when any of the names only matched case-insensitively.
type folderMatch struct {
	folder openrelik.Folder
	path   []string
	used   int
	folded bool
}

func (m folderMatch) location() string { return formatFolderPath(m.path) }

func formatFolderPath(names []string) string {
	return "/" + strings.Join(names, "/") + "/"
}

// notFoundError is returned when no folder or file matches a path, as
// opposed to the server failing to answer.
type notFoundError string

func (e notFoundError) Error() string { return string(e) }

// resolveFolderPath walks a path like "Case A/evidence/" from the root
// folders. Every branch whose names match is followed, so a path is only
// ambiguous if more than one complete path matches. Folder names may contain
// '/', so a name can match several segments: "foo/bar/123" finds a root
// folder named "foo/bar/123" as well as folder 123 inside foo/bar.
// Names match case-insensitively, but paths that match exactly win.
func resolveFolderPath(ctx context.Context, client *openrelik.Client, path string) (int, error) {
	segments := pathSegments(path)
	if len(segments) == 0 {
		return 0, usageErrorf("invalid folder path %q", path)
	}

	reached, err := walkFolderPath(ctx, client, segments)
	if err != nil {
		return 0, err
	}
	var matches []folderMatch
	for _, m := range reached {
		if m.used == len(segments) {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		return 0, folderNotFound(segments, reached)
	}
	matches = preferExact(matches, func(m folderMatch) bool { return m.folded })
	if len(matches) > 1 {
		return 0, ambiguousFolderError(path, matches)
	}
	return matches[0].folder.ID, nil
}

// walkFolderPath follows every branch of folders whose names match
// segments, starting at the root folders, and returns every folder reached,
// including those that match only the start of segments.
func walkFolderPath(ctx context.Context, client *openrelik.Client, segments []string) ([]folderMatch, error) {
	roots, err := util.ListAllRootFolders(ctx, client)
	if err != nil {
		return nil, err
	}
	pending := matchFolderNames(roots, segments, folderMatch{})
	var reached []folderMatch
	for len(pending) > 0 {
		var next []folderMatch
		for _, m := range pending {
			reached = append(reached, m)
			if m.used == len(segments) {
				continue
			}
			subfolders, _, err := client.Folders().ListSubFolders(ctx, m.folder.ID)
			if err != nil {
				return nil, err
			}
			next = append(next, matchFolderNames(subfolders, segments, m)...)
		}
		pending = next
	}
	return reached, nil
}

// folderNotFound reports how far along segments the folders reached got.
func folderNotFound(segments []string, reached []folderMatch) error {
	matched := 0
	for _, m := range reached {
		matched = max(matched, m.used)
	}
	if matched == 0 {
		return notFoundError(fmt.Sprintf("no root folder named %q", segments[0]))
	}
	return notFoundError(fmt.Sprintf("no folder named %q in %s", segments[matched], formatFolderPath(segments[:matched])))
}

// preferExact drops the matches that needed case folding, unless all of
// them did.
func preferExact[T any](matches []T, folded func(T) bool) []T {
	if exact := slices.DeleteFunc(slices.Clone(matches), folded); len(exact) > 0 {
		return exact
	}
	return matches
}

// pathSegments splits a path on '/', dropping empty segments.
func pathSegments(path string) []string {
	var segments []string
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	return segments
}

// matchFolderNames returns the folders whose names match, ignoring case,
// the path segments following those parent used up.
func matchFolderNames(folders []openrelik.Folder, segments []string, parent folderMatch) []folderMatch {
	rest := segments[parent.used:]
	var matches []folderMatch
	for _, f := range folders {
		name := pathSegments(f.DisplayName)
		if len(name) == 0 || len(name) > len(rest) || !slices.EqualFunc(name, rest[:len(name)], strings.EqualFold) {
			continue
		}
		matches = append(matches, folderMatch{
			folder: f,
			path:   append(slices.Clone(parent.path), f.DisplayName),
			used:   parent.used + len(name),
			folded: parent.folded || !slices.Equal(name, rest[:len(name)]),
		})
	}
	return matches
}

// ambiguousFolderError lists the folders matching path so the user can
// pick one by ID.
func ambiguousFolderError(path string, matches []folderMatch) error {
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].folder.UpdatedAt.Before(matches[j].folder.UpdatedAt)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "multiple folders match %q:\n\n", path)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLOCATION\tOWNER\tUPDATED")
	for _, m := range matches {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", m.folder.ID, m.location(),
			m.folder.User.Username, util.FormatTime(m.folder.UpdatedAt))
	}
	tw.Flush()
	b.WriteString("\nUse one of the IDs above instead of the path.")
	return errors.New(b.String())
}

// fetchFolderContents fetches the subfolders and files of a folder
// concurrently.
func fetchFolderContents(ctx context.Context, client *openrelik.Client, folderID int) ([]openrelik.Folder, []openrelik.FolderFile, error) {
	var (
		wg                 sync.WaitGroup
		folders            []openrelik.Folder
		files              []openrelik.FolderFile
		folderErr, fileErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		folders, _, folderErr = client.Folders().ListSubFolders(ctx, folderID)
	}()
	go func() {
		defer wg.Done()
		files, _, fileErr = client.Folders().ListFiles(ctx, folderID)
	}()
	wg.Wait()

	// Check the subfolder error first so a missing folder reports consistently.
	if folderErr != nil {
		return nil, nil, folderErr
	}
	if fileErr != nil {
		return nil, nil, fileErr
	}
	return folders, files, nil
}

// listEntry is a single row in a folder listing: either a folder or a file.
type listEntry struct {
	folder *openrelik.Folder
	file   *openrelik.FolderFile
}

func (e listEntry) updated() time.Time {
	if e.folder != nil {
		return e.folder.UpdatedAt
	}
	return e.file.UpdatedAt
}

func (e listEntry) name() string {
	if e.folder != nil {
		return e.folder.DisplayName
	}
	return e.file.DisplayName
}

func (e listEntry) id() int {
	if e.folder != nil {
		return e.folder.ID
	}
	return e.file.ID
}

// kindRank orders folders before files when entries otherwise tie.
func (e listEntry) kindRank() int {
	if e.folder != nil {
		return 0
	}
	return 1
}

// limitAndSortEntries keeps the limit most recently updated entries (all of
// them if limit is 0), then sorts them by sortBy.
func limitAndSortEntries(entries []listEntry, limit int, sortBy string, reverse bool) []listEntry {
	if limit > 0 && len(entries) > limit {
		sortEntries(entries, "time", true)
		entries = entries[:limit]
	}
	sortEntries(entries, sortBy, reverse)
	return entries
}

// sortEntries sorts entries like 'ls -ltr': by last update with the oldest
// first, or by name when sortBy is "name". reverse inverts the order. Kind
// (folders first) and then ID break ties so the order is stable.
func sortEntries(entries []listEntry, sortBy string, reverse bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if reverse {
			a, b = b, a
		}
		switch sortBy {
		case "name":
			if an, bn := strings.ToLower(a.name()), strings.ToLower(b.name()); an != bn {
				return an < bn
			}
		default:
			if !a.updated().Equal(b.updated()) {
				return a.updated().Before(b.updated())
			}
		}
		if a.kindRank() != b.kindRank() {
			return a.kindRank() < b.kindRank()
		}
		return a.id() < b.id()
	})
}

func newCreateFolderCmd() *cobra.Command {
	var parent string
	var existOK bool
	var latest bool

	cmd := &cobra.Command{
		Use:   "create <NAME>",
		Short: "Create a folder",
		Long: `Create a new folder in OpenRelik.

Without --parent, creates a root-level folder. With --parent, creates a
subfolder inside the specified parent folder. The parent is a folder ID
or a path of folder names, as accepted by 'folder list'.

With --exist-ok, if a folder with exactly this name already exists in that
location, it is printed instead of creating a duplicate; at the root, only
folders you own count. If several such folders exist, the command fails
and lists them; add --latest to use the most recently updated one instead.
This makes scripts safe to re-run, but it is not atomic: two runs at the
same moment can still both create the folder.`,
		Example: `  # Create a root folder
  openrelik folder create "Investigations"

  # Create a subfolder inside folder 42
  openrelik folder create "Case 2024-001" --parent 42

  # Create a subfolder by parent path
  openrelik folder create "logs" --parent "Case A/evidence/"

  # Get the ID of "Case 7", creating it only if it doesn't exist
  openrelik folder create "Case 7" --exist-ok --format json | jq .id

  # Same, using the most recently updated "Case 7" if there are several
  openrelik folder create "Case 7" --exist-ok --latest`,
		Args: util.UseArgsWithQuoteHint("parent"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if latest && !existOK {
				return fmt.Errorf("--latest can only be used with --exist-ok")
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			name := args[0]
			var parentID int
			if cmd.Flags().Changed("parent") {
				parentID, err = resolveFolder(cmd.Context(), client, parent)
				if err != nil {
					return err
				}
			}

			var folder *openrelik.Folder
			created := true
			switch {
			case existOK:
				folder, created, err = util.FindOrCreateFolder(cmd.Context(), client, parentID, name)
				var multiple *util.MultipleFoldersError
				if errors.As(err, &multiple) {
					if !latest {
						return existingFoldersError(multiple)
					}
					folder, err = util.LatestFolder(multiple.Matches), nil
					if !quiet && outputFormat == "human" {
						fmt.Fprintf(cmd.ErrOrStderr(), "%d folders named %q exist; using the most recently updated.\n",
							len(multiple.Matches), name)
					}
				}
			case parentID != 0:
				folder, _, err = client.Folders().CreateSubFolder(cmd.Context(), parentID, name)
			default:
				folder, _, err = client.Folders().CreateRootFolder(cmd.Context(), name)
			}

			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &FolderCreatedView{Folder: folder, Created: created})
		},
	}

	cmd.Flags().StringVarP(&parent, "parent", "p", "", "Parent folder ID or path")
	cmd.Flags().BoolVar(&existOK, "exist-ok", false, "If a folder with this name already exists here, print it instead of creating a duplicate")
	cmd.Flags().BoolVar(&latest, "latest", false, "With --exist-ok, use the most recently updated folder if several exist")
	return cmd
}

// existingFoldersError lists the folders that made 'folder create
// --exist-ok' ambiguous so the user can pick one by ID.
func existingFoldersError(e *util.MultipleFoldersError) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:\n\n", e.Error())
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tOWNER\tUPDATED")
	for _, f := range e.Matches {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", f.ID, f.User.Username, util.FormatTime(f.UpdatedAt))
	}
	tw.Flush()
	b.WriteString("\nUse one of the IDs above, or add --latest to use the most recently updated.")
	return errors.New(b.String())
}

// searchPathConcurrency is the number of folder lookups run in parallel to
// find the paths of search results.
const searchPathConcurrency = 8

func newSearchFoldersCmd() *cobra.Command {
	var opts listOptions
	var rootOnly, latest bool

	cmd := &cobra.Command{
		Use:   "search <TERM>",
		Short: "Search folders by name",
		Long: `Search every folder you can access, including subfolders and folders
shared with you, for names containing TERM (case-insensitive).

Results show each folder's full path from the root folders, such as
"/Case A/evidence/". The path can be passed as it is to commands that take
a folder, such as 'folder list'; the leading and trailing "/" are optional,
so "Case A/evidence" is the same folder.

Like 'folder list', results are sorted by last update with the most
recently updated folder last; use --sort name and -r to change the order.
All matches are shown; use --limit N to show only the N most recently
updated. Use --latest to show only the most recently updated match (the
same as --limit 1).

Use --root to search only the folders that 'folder list' shows without a
folder: your root folders and folders shared with you.`,
		Example: `  # Find folders with "plaso" in their name
  openrelik folder search plaso

  # Quote terms that contain spaces
  openrelik folder search "untitled workflow"

  # Show the 10 most recently updated matches, sorted by name
  openrelik folder search case --limit 10 --sort name

  # Find the most recently updated root folder with "case" in its name
  openrelik folder search case --root --latest

  # Print the IDs of matching folders
  openrelik folder search evidence --format json | jq '.[].id'`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			term := strings.TrimSpace(args[0])
			if term == "" {
				return fmt.Errorf("search term must not be empty")
			}
			if latest {
				opts.limit = 1
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			var folders []openrelik.Folder
			if rootOnly {
				folders, err = searchRootFolders(cmd.Context(), client, term)
			} else {
				folders, err = fetchFolderSearch(cmd.Context(), client, term)
			}
			if err != nil {
				return err
			}

			entries := make([]listEntry, len(folders))
			for i := range folders {
				entries[i] = listEntry{folder: &folders[i]}
			}
			total := len(entries)
			entries = limitAndSortEntries(entries, opts.limit, opts.sortBy, opts.reverse)

			// Paths cost one request per folder, so only look up the shown ones.
			paths, err := fetchFolderPaths(cmd.Context(), client, entries)
			if err != nil {
				return err
			}

			if total == 0 && !quiet && outputFormat == "human" {
				what := "folders"
				if rootOnly {
					what = "root folders"
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "No %s match %q.\n", what, term)
			}
			view := &FolderSearchView{}
			for _, e := range entries {
				view.Folders = append(view.Folders, searchFolder{Folder: *e.folder, Path: paths[e.folder.ID]})
			}
			if err := formatAndPrint(cmd, view); err != nil {
				return err
			}
			if !latest {
				printTruncationHint(cmd, len(entries), total, "folders", "Omit --limit to see all.")
			}
			return nil
		},
	}

	opts.addFlags(cmd, 0)
	cmd.Flags().BoolVar(&rootOnly, "root", false, "Search only root folders and folders shared with you")
	cmd.Flags().BoolVar(&latest, "latest", false, "Show only the most recently updated match (same as --limit 1)")
	cmd.MarkFlagsMutuallyExclusive("latest", "limit")
	return cmd
}

// searchRootFolders returns the folders 'folder list' shows at the top level
// whose names contain term, ignoring case like the server's search.
func searchRootFolders(ctx context.Context, client *openrelik.Client, term string) ([]openrelik.Folder, error) {
	roots, err := util.ListAllRootFolders(ctx, client)
	if err != nil {
		return nil, err
	}
	term = strings.ToLower(term)
	var matches []openrelik.Folder
	for _, f := range roots {
		if strings.Contains(strings.ToLower(f.DisplayName), term) {
			matches = append(matches, f)
		}
	}
	return matches, nil
}

// fetchFolderSearch pages through the folder search results for term.
func fetchFolderSearch(ctx context.Context, client *openrelik.Client, term string) ([]openrelik.Folder, error) {
	var folders []openrelik.Folder
	for page := 1; ; page++ {
		result, _, err := client.Folders().SearchFolders(ctx, term, &openrelik.PageOptions{Page: page, PageSize: folderPageSize})
		if err != nil {
			return nil, err
		}
		folders = append(folders, result.Folders...)
		if len(result.Folders) == 0 || len(folders) >= result.TotalCount {
			return folders, nil
		}
	}
}

// fetchFolderPaths looks up the full path of each folder entry, keyed by
// folder ID, running a few lookups at a time. The first error cancels the
// lookups still in flight.
func fetchFolderPaths(ctx context.Context, client *openrelik.Client, entries []listEntry) (map[int]string, error) {
	var mu sync.Mutex
	paths := make(map[int]string, len(entries))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(searchPathConcurrency)
	for _, e := range entries {
		id := e.folder.ID
		g.Go(func() error {
			folder, _, err := client.Folders().Get(ctx, id)
			if err != nil {
				return fmt.Errorf("failed to look up folder %d: %w", id, err)
			}
			mu.Lock()
			paths[id] = folderPath(folder)
			mu.Unlock()
			return nil
		})
	}
	return paths, g.Wait()
}

// folderPath builds a folder's path, like "/Case A/evidence/", from its
// chain of parents.
func folderPath(f *openrelik.Folder) string {
	var names []string
	for ; f != nil; f = f.Parent {
		names = append([]string{f.DisplayName}, names...)
	}
	return formatFolderPath(names)
}
