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
)

func newFolderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "folder",
		Aliases: []string{"folders"},
		Short:   "Manage folders",
		Long:    `Create, list, and search folders in OpenRelik.`,
	}

	cmd.AddCommand(newListFoldersCmd())
	cmd.AddCommand(newCreateFolderCmd())
	cmd.AddCommand(newSearchFoldersCmd())
	return cmd
}

// folderPageSize is the number of folders requested per API call.
const folderPageSize = 100

func newListFoldersCmd() *cobra.Command {
	var opts listOptions

	cmd := &cobra.Command{
		Use:     "list [FOLDER]",
		Aliases: []string{"ls"},
		Short:   "List folder contents",
		Long: `List folder contents in OpenRelik.

Without FOLDER, lists all root folders. With FOLDER, lists the
subfolders and files in that folder.

FOLDER is either a folder ID or a path of folder names starting at the
root folders, such as 'Case A/evidence/'. An argument made only of
digits is an ID; add a trailing '/' to treat it as a name instead
(e.g. '2024/'). If more than one folder matches a path, the matches are
listed so you can pick one by ID.

Folder names end in '/' and have TYPE folder. Folder and file IDs are
separate, so pass folder IDs to 'folder list' and file IDs to commands
such as 'file info'.

Like 'ls -latr', entries are sorted by last update with the most
recently updated entry last. Use --sort name to sort by name and
-r to reverse the sort order. Use --limit N to show only the N most
recently updated entries, in the selected sort order.`,
		Example: `  # List all root folders, most recently updated last
  openrelik folder list

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
			if fID != 0 {
				folders, files, err = fetchFolderContents(cmd.Context(), client, fID)
				noun = "items"
			} else {
				folders, err = fetchAllRootFolders(cmd.Context(), client)
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

			if err := formatAndPrint(cmd, &FolderContentsView{Entries: entries}); err != nil {
				return err
			}
			printTruncationHint(cmd, len(entries), total, noun, "Omit --limit to see all.")
			return nil
		},
	}

	opts.addFlags(cmd, 0)
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
		return fmt.Errorf("invalid limit: %d (must be 0 or greater)", o.limit)
	}
	if o.sortBy != "time" && o.sortBy != "name" {
		return fmt.Errorf("invalid sort: %q (must be time or name)", o.sortBy)
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
			return 0, fmt.Errorf("invalid folder ID: %s", arg)
		}
		return id, nil
	}
	return resolveFolderPath(ctx, client, arg)
}

// folderMatch is a folder found while resolving a path, with the names of
// the folders leading to it.
type folderMatch struct {
	folder openrelik.Folder
	path   []string
}

func (m folderMatch) location() string { return formatFolderPath(m.path) }

func formatFolderPath(names []string) string {
	return "/" + strings.Join(names, "/") + "/"
}

// resolveFolderPath walks a path like "Case A/evidence/" from the root
// folders. Every branch whose names match is followed, so a path is only
// ambiguous if more than one complete path matches.
func resolveFolderPath(ctx context.Context, client *openrelik.Client, path string) (int, error) {
	var segments []string
	for _, seg := range strings.Split(path, "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	if len(segments) == 0 {
		return 0, fmt.Errorf("invalid folder path %q", path)
	}

	roots, err := fetchAllRootFolders(ctx, client)
	if err != nil {
		return 0, err
	}
	matches := matchFolderName(roots, segments[0], nil)
	if len(matches) == 0 {
		return 0, fmt.Errorf("no root folder named %q", segments[0])
	}

	for i, seg := range segments[1:] {
		var next []folderMatch
		for _, m := range matches {
			subfolders, _, err := client.Folders().ListSubFolders(ctx, m.folder.ID)
			if err != nil {
				return 0, err
			}
			next = append(next, matchFolderName(subfolders, seg, m.path)...)
		}
		if len(next) == 0 {
			return 0, fmt.Errorf("no folder named %q in %s", seg, formatFolderPath(segments[:i+1]))
		}
		matches = next
	}

	if len(matches) > 1 {
		return 0, ambiguousFolderError(path, matches)
	}
	return matches[0].folder.ID, nil
}

// matchFolderName returns the folders named name, falling back to a
// case-insensitive match when no name matches exactly.
func matchFolderName(folders []openrelik.Folder, name string, parent []string) []folderMatch {
	var exact, folded []folderMatch
	for _, f := range folders {
		m := folderMatch{folder: f, path: append(slices.Clone(parent), f.DisplayName)}
		switch {
		case f.DisplayName == name:
			exact = append(exact, m)
		case strings.EqualFold(f.DisplayName, name):
			folded = append(folded, m)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return folded
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
			m.folder.User.Username, util.FormatTimeAgo(m.folder.UpdatedAt))
	}
	tw.Flush()
	b.WriteString("\nRun 'openrelik folder list <ID>' with one of the IDs above.")
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

// fetchAllRootFolders pages through the root folders endpoint and returns
// every root folder.
func fetchAllRootFolders(ctx context.Context, client *openrelik.Client) ([]openrelik.Folder, error) {
	var folders []openrelik.Folder
	for page := 1; ; page++ {
		result, _, err := client.Folders().ListRootFoldersPage(ctx, &openrelik.ListOptions{Page: page, PageSize: folderPageSize})
		if err != nil {
			return nil, err
		}
		folders = append(folders, result.Folders...)
		if len(result.Folders) == 0 || len(folders) >= result.TotalCount {
			return folders, nil
		}
	}
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

	cmd := &cobra.Command{
		Use:   "create <NAME>",
		Short: "Create a folder",
		Long: `Create a new folder in OpenRelik.

Without --parent, creates a root-level folder. With --parent, creates a
subfolder inside the specified parent folder. The parent is a folder ID
or a path of folder names, as accepted by 'folder list'.`,
		Example: `  # Create a root folder
  openrelik folder create "Investigations"

  # Create a subfolder inside folder 42
  openrelik folder create "Case 2024-001" --parent 42

  # Create a subfolder by parent path
  openrelik folder create "logs" --parent "Case A/evidence/"`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}

			name := args[0]
			var folder *openrelik.Folder
			if cmd.Flags().Changed("parent") {
				parentID, err := resolveFolder(cmd.Context(), client, parent)
				if err != nil {
					return err
				}
				folder, _, err = client.Folders().CreateSubFolder(cmd.Context(), parentID, name)
			} else {
				folder, _, err = client.Folders().CreateRootFolder(cmd.Context(), name)
			}

			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &FolderCreatedView{Folder: folder})
		},
	}

	cmd.Flags().StringVarP(&parent, "parent", "p", "", "Parent folder ID or path")
	return cmd
}

// searchPathConcurrency is the number of folder lookups run in parallel to
// find the paths of search results.
const searchPathConcurrency = 8

func newSearchFoldersCmd() *cobra.Command {
	var opts listOptions

	cmd := &cobra.Command{
		Use:   "search <TERM>",
		Short: "Search folders by name",
		Long: `Search every folder you can access, including subfolders and folders
shared with you, for names containing TERM (case-insensitive).

Results show each folder's full path, which can be passed to commands
that take a folder, such as 'folder list'.

Like 'folder list', results are sorted by last update with the most
recently updated folder last; use --sort name and -r to change the order.
By default the 30 most recently updated matches are shown; use --limit N
to show more, or --limit 0 to show all.`,
		Example: `  # Find folders with "plaso" in their name
  openrelik folder search plaso

  # Quote terms that contain spaces
  openrelik folder search "untitled workflow"

  # Show all matches, sorted by name
  openrelik folder search case --limit 0 --sort name

  # Print the IDs of matching folders
  openrelik folder search evidence --format json | jq '.[].id'`,
		Args:         util.UseArgsWithQuoteHint(),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			term := strings.TrimSpace(args[0])
			if term == "" {
				return fmt.Errorf("search term must not be empty")
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			folders, err := fetchFolderSearch(cmd.Context(), client, term)
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
				fmt.Fprintf(cmd.ErrOrStderr(), "No folders match %q.\n", term)
			}
			view := &FolderSearchView{}
			for _, e := range entries {
				view.Folders = append(view.Folders, searchFolder{Folder: *e.folder, Path: paths[e.folder.ID]})
			}
			if err := formatAndPrint(cmd, view); err != nil {
				return err
			}
			printTruncationHint(cmd, len(entries), total, "folders", "Use --limit N to see more, or --limit 0 for all.")
			return nil
		},
	}

	opts.addFlags(cmd, 30)
	return cmd
}

// fetchFolderSearch pages through the folder search results for term.
func fetchFolderSearch(ctx context.Context, client *openrelik.Client, term string) ([]openrelik.Folder, error) {
	var folders []openrelik.Folder
	for page := 1; ; page++ {
		result, _, err := client.Folders().SearchFolders(ctx, term, &openrelik.ListOptions{Page: page, PageSize: folderPageSize})
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
// folder ID, running a few lookups at a time.
func fetchFolderPaths(ctx context.Context, client *openrelik.Client, entries []listEntry) (map[int]string, error) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		paths    = make(map[int]string, len(entries))
		sem      = make(chan struct{}, searchPathConcurrency)
	)
	for _, e := range entries {
		id := e.folder.ID
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			folder, _, err := client.Folders().Get(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to look up folder %d: %w", id, err)
				}
				return
			}
			paths[id] = folderPath(folder)
		}()
	}
	wg.Wait()
	return paths, firstErr
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
