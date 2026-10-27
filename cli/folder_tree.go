package cli

import (
	"context"
	"fmt"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// treeFetchConcurrency is the number of folders whose contents are fetched in
// parallel while building a tree.
const treeFetchConcurrency = 8

func newTreeFoldersCmd() *cobra.Command {
	var opts listOptions
	var level int
	var foldersOnly bool
	var showSize bool

	cmd := &cobra.Command{
		Use:   "tree [FOLDER]",
		Short: "List folder contents as a tree",
		Long: `List the contents of a folder recursively as a tree, like 'tree'.

Without FOLDER, shows all root folders and everything below them. FOLDER is
a folder ID or a path of folder names, as accepted by 'folder list'. Each
entry shows its ID in brackets; folder names end in '/'.

Entries are sorted by name; use --sort time to sort by last update (oldest
first) and -r to reverse the order. Use --level N to limit the depth and
--folders-only to leave out files, which also makes large trees faster to
fetch. Use --size to show file sizes.`,
		Example: `  # Show everything below folder 42
  openrelik folder tree 42

  # Show a folder by path, two levels deep
  openrelik folder tree "Case A/" --level 2

  # Show file sizes
  openrelik folder tree 42 --size

  # Show only the folder structure of all root folders
  openrelik folder tree --folders-only`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if level < 0 {
				return fmt.Errorf("invalid level: %d (must be 0 or greater)", level)
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			root := &treeNode{}
			var frontier []*treeNode
			depth := 1
			if len(args) > 0 {
				fID, err := resolveFolder(cmd.Context(), client, args[0])
				if err != nil {
					return err
				}
				folder, _, err := client.Folders().Get(cmd.Context(), fID)
				if err != nil {
					return err
				}
				folder.Parent = nil
				root.folder = folder
				frontier = []*treeNode{root}
			} else {
				folders, err := util.ListAllRootFolders(cmd.Context(), client)
				if err != nil {
					return err
				}
				// The root folders form the first level of the tree.
				root.setContents(folders, []openrelik.FolderFile{})
				frontier = root.subfolders()
				depth = 2
			}

			for ; len(frontier) > 0 && (level == 0 || depth <= level); depth++ {
				if err := fetchTreeLevel(cmd.Context(), client, frontier, foldersOnly); err != nil {
					return err
				}
				var next []*treeNode
				for _, n := range frontier {
					next = append(next, n.subfolders()...)
				}
				frontier = next
			}

			root.sort(opts.sortBy, opts.reverse)
			return formatAndPrint(cmd, &FolderTreeView{Root: root, FoldersOnly: foldersOnly, ShowSize: showSize})
		},
	}

	cmd.Flags().StringVar(&opts.sortBy, "sort", "name", "Sort by: name or time (oldest first)")
	cmd.Flags().BoolVarP(&opts.reverse, "reverse", "r", false, "Reverse the sort order")
	cmd.Flags().IntVar(&level, "level", 0, "Maximum depth to show (0 shows all levels)")
	cmd.Flags().BoolVar(&foldersOnly, "folders-only", false, "Show folders only, not files")
	cmd.Flags().BoolVar(&showSize, "size", false, "Show file sizes")
	return cmd
}

// treeNode is a folder in a tree, or the virtual root above the root folders
// when folder is nil. entries is nil until the folder's contents are fetched.
type treeNode struct {
	folder   *openrelik.Folder
	entries  []listEntry
	children map[int]*treeNode
}

// setContents records a folder's subfolders and files. A nil files slice
// means files were not fetched.
func (n *treeNode) setContents(folders []openrelik.Folder, files []openrelik.FolderFile) {
	n.entries = make([]listEntry, 0, len(folders)+len(files))
	n.children = make(map[int]*treeNode, len(folders))
	for i := range folders {
		n.entries = append(n.entries, listEntry{folder: &folders[i]})
		n.children[folders[i].ID] = &treeNode{folder: &folders[i]}
	}
	for i := range files {
		n.entries = append(n.entries, listEntry{file: &files[i]})
	}
}

// subfolders returns the child folder nodes in entry order.
func (n *treeNode) subfolders() []*treeNode {
	var out []*treeNode
	for _, e := range n.entries {
		if e.folder != nil {
			out = append(out, n.children[e.folder.ID])
		}
	}
	return out
}

// sort orders the entries of n and every folder below it.
func (n *treeNode) sort(sortBy string, reverse bool) {
	sortEntries(n.entries, sortBy, reverse)
	for _, c := range n.children {
		c.sort(sortBy, reverse)
	}
}

// fetchTreeLevel fetches the contents of every node in level, a few folders
// at a time. The first error cancels the fetches still in flight.
func fetchTreeLevel(ctx context.Context, client *openrelik.Client, level []*treeNode, foldersOnly bool) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(treeFetchConcurrency)
	for _, n := range level {
		g.Go(func() error {
			var folders []openrelik.Folder
			var files []openrelik.FolderFile
			var err error
			if foldersOnly {
				folders, _, err = client.Folders().ListSubFolders(ctx, n.folder.ID)
			} else {
				folders, files, err = fetchFolderContents(ctx, client, n.folder.ID)
			}
			if err != nil {
				return fmt.Errorf("failed to list folder %d: %w", n.folder.ID, err)
			}
			// Each goroutine writes only its own node.
			if !foldersOnly && files == nil {
				files = []openrelik.FolderFile{}
			}
			n.setContents(folders, files)
			return nil
		})
	}
	return g.Wait()
}
