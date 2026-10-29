package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// binaryPeekSize is how many leading bytes are checked for a NUL byte when
// deciding whether a file is binary, as git and less do.
const binaryPeekSize = 8192

// stdoutIsTerminal reports whether w is a terminal. Tests override it.
var stdoutIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// stdinIsTerminal reports whether r is a terminal. Tests override it.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "file",
		Aliases: []string{"files"},
		Short:   "Manage files",
		Long:    `Inspect and print files stored in OpenRelik.`,
	}

	cmd.AddCommand(newFileInfoCmd())
	cmd.AddCommand(newFileCatCmd())
	cmd.AddCommand(removedFileCmds()...)
	cmd.AddCommand(newFileShareCmds()...)
	return cmd
}

func newFileInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <FILE>",
		Short: "Get file metadata",
		Long: `Display detailed metadata for a file, including size, hashes, MIME type,
and timestamps.

FILE is either a file ID, as shown by 'folder list <FOLDER>', or a path
of folder names ending in the file name, such as 'Case A/evidence/notes.txt'.
File names may contain '/' too: 'Case A/2024/report.txt' also finds a
file named "2024/report.txt" in Case A.
If more than one file matches a path, the matches are listed so you can
pick one by ID.

Every part of a path is a name, starting at the root folders, so
'42/notes.txt' looks in a root folder named "42", not in folder 42; use
the file ID instead.`,
		Example: `  # Show metadata for file 123
  openrelik file info 123

  # Show metadata for a file by its path
  openrelik file info "Case A/evidence/notes.txt"

  # Output as JSON
  openrelik file info 123 --format json`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}

			fileID, err := resolveFile(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			file, _, err := client.Files().Info(cmd.Context(), fileID)
			if err != nil {
				return err
			}

			return formatAndPrint(cmd, &FileInfoView{File: file})
		},
	}
}

func newFileCatCmd() *cobra.Command {
	var number bool
	var maxBytes string

	cmd := &cobra.Command{
		Use:   "cat <FILE> [FILE...]",
		Short: "Print file contents",
		Long: `Print the contents of one or more files to stdout. Several files are
concatenated in the order given, and the command stops at the first failure.

FILE is either a file ID, as shown by 'folder list <FOLDER>', or a path
of folder names ending in the file name, such as 'Case A/evidence/notes.txt'.
File names may contain '/' too: 'Case A/2024/report.txt' also finds a
file named "2024/report.txt" in Case A.
Folders in the path are matched as by 'folder list'. If more than one file
matches a path, the matches are listed so you can pick one by ID.
Every part of a path is a name, starting at the root folders, so
'42/notes.txt' looks in a root folder named "42", not in folder 42; use
the file ID instead.

With --bytes N, only the first N bytes of each file are printed, and the
download stops once they have been read.

Files that look binary are refused when stdout is a terminal. Pipe or
redirect the output to print them anyway.`,
		Example: `  # Search a file
  openrelik file cat 123 | grep -i password

  # Show the first 50 lines
  openrelik file cat 123 | head -50

  # Number the lines
  openrelik file cat -n 123

  # Look at the header of a large disk image
  openrelik file cat -c 512 123 | xxd

  # Print a file by its path
  openrelik file cat "Case A/evidence/notes.txt"

  # Join several files
  openrelik file cat 123 124 > combined.txt`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit := int64(-1)
			if cmd.Flags().Changed("bytes") {
				var err error
				if limit, err = util.ParseBytes(maxBytes); err != nil {
					return err
				}
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			// Resolve every argument before printing anything, so a bad
			// argument doesn't leave partial output behind.
			fileIDs := make([]int, len(args))
			for i, arg := range args {
				fileIDs[i], err = resolveFile(cmd.Context(), client, arg)
				if err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			tty := stdoutIsTerminal(out)

			// Numbering writes each line in pieces, so buffer the output
			// to avoid a write per piece.
			var bw *bufio.Writer
			if number {
				bw = bufio.NewWriter(out)
				out = &lineNumberWriter{w: bw}
			}
			flush := func() error {
				if bw == nil {
					return nil
				}
				if err := bw.Flush(); err != nil && !errors.Is(err, syscall.EPIPE) {
					return err
				}
				return nil
			}

			for _, id := range fileIDs {
				if err := catFile(cmd.Context(), client, out, id, limit, tty); err != nil {
					flush()
					return fmt.Errorf("file %d: %w", id, err)
				}
			}
			return flush()
		},
	}

	cmd.Flags().BoolVarP(&number, "number", "n", false, "Number all output lines, like 'cat -n'")
	cmd.Flags().StringVarP(&maxBytes, "bytes", "c", "", "Print only the first N bytes of each file (e.g. 512, 4K, 10M)")
	return cmd
}

// lineNumberWriter prefixes every line written to w with its line number,
// formatted like 'cat -n'. Numbering continues across writes, so a line
// split between two files is numbered once.
type lineNumberWriter struct {
	w       io.Writer
	line    int
	midLine bool
}

func (l *lineNumberWriter) Write(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if !l.midLine {
			l.line++
			if _, err := fmt.Fprintf(l.w, "%6d\t", l.line); err != nil {
				return n, err
			}
			l.midLine = true
		}
		end := len(p)
		if i := bytes.IndexByte(p[n:], '\n'); i >= 0 {
			end = n + i + 1
			l.midLine = false
		}
		written, err := l.w.Write(p[n:end])
		n += written
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// resolveFile turns a file argument into a file ID. Arguments made only of
// digits are IDs; anything else is a path like "Case A/evidence/notes.txt",
// resolved by resolveFilePath.
func resolveFile(ctx context.Context, client *openrelik.Client, arg string) (int, error) {
	if folderIDPattern.MatchString(arg) {
		id, err := strconv.Atoi(arg)
		if err != nil || id == 0 {
			return 0, usageErrorf("invalid file ID: %s", arg)
		}
		return id, nil
	}
	m, err := resolveFilePath(ctx, client, nil, arg)
	if err != nil {
		return 0, err
	}
	return m.file.ID, nil
}

// fileMatch is a file found while resolving a path, with the folder it is
// in. inexact is set when any of the names matched only after ignoring case
// or extra '/' in the name.
type fileMatch struct {
	file    openrelik.FolderFile
	folder  folderMatch
	inexact bool
}

// resolveFilePath resolves a path whose folders are resolved as by 'folder
// list' and whose last segment is the file name. File names may contain '/'
// too, so every folder the path leads through is checked for a file named
// the rest of the path: "Case A/a/b.txt" finds a file named "a/b.txt" in
// Case A as well as b.txt in Case A/a/. As for folders, names match
// ignoring case and extra '/', but paths whose names match exactly win.
// If base isn't nil, the folders are resolved from its subfolders.
func resolveFilePath(ctx context.Context, client *openrelik.Client, base *openrelik.Folder, arg string) (fileMatch, error) {
	i := strings.LastIndex(arg, "/")
	if i < 0 || strings.Trim(arg[:i], "/") == "" {
		return fileMatch{}, usageErrorf("invalid file %q: give a file ID or a path such as 'FOLDER/NAME'", arg)
	}
	name := arg[i+1:]
	if name == "" {
		return fileMatch{}, usageErrorf("invalid file %q: the path ends in '/', so it names a folder", arg)
	}

	// The folders leave at least the last segment for the file name.
	segments := pathSegments(arg)
	dirSegments := segments[:len(segments)-1]
	folders, err := walkFolderPath(ctx, client, base, dirSegments)
	if err != nil {
		return fileMatch{}, err
	}
	var matches []fileMatch
	for _, folder := range folders {
		files, _, err := client.Folders().ListFiles(ctx, folder.folder.ID)
		if err != nil {
			return fileMatch{}, err
		}
		rest := segments[folder.used:]
		exactName := strings.Join(rest, "/")
		for _, f := range files {
			if slices.EqualFunc(pathSegments(f.DisplayName), rest, strings.EqualFold) {
				matches = append(matches, fileMatch{
					file:    f,
					folder:  folder,
					inexact: folder.inexact || f.DisplayName != exactName,
				})
			}
		}
	}

	if len(matches) == 0 {
		if !slices.ContainsFunc(folders, func(m folderMatch) bool { return m.used == len(dirSegments) }) {
			return fileMatch{}, folderNotFound(base, dirSegments, folders)
		}
		if base != nil {
			path := formatFolderPath(append([]string{base.DisplayName}, dirSegments...))
			return fileMatch{}, notFoundError(fmt.Sprintf("no file named %q in %s of case %s", name, path, base.CaseRef))
		}
		return fileMatch{}, notFoundError(fmt.Sprintf("no file named %q in %s", name, formatFolderPath(dirSegments)))
	}
	matches = preferExact(matches, func(m fileMatch) bool { return m.inexact })
	if len(matches) > 1 {
		return fileMatch{}, ambiguousFileError(arg, matches)
	}
	return matches[0], nil
}

// ambiguousFileError lists the files matching path so the user can pick
// one by ID.
func ambiguousFileError(path string, matches []fileMatch) error {
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].file.UpdatedAt.Before(matches[j].file.UpdatedAt)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "multiple files match %q:\n\n", path)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLOCATION\tNAME\tSIZE\tUPDATED")
	for _, m := range matches {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", m.file.ID, m.folder.location(), m.file.DisplayName,
			util.FormatBytes(m.file.Filesize), util.FormatTime(m.file.UpdatedAt))
	}
	tw.Flush()
	b.WriteString("\nUse one of the IDs above instead of the path.")
	return errors.New(b.String())
}

// catFile streams the contents of file id to out. If limit is not negative,
// only the first limit bytes are read; closing the body then stops the
// download. When tty is true, files that look binary are refused instead of
// being written to the terminal.
func catFile(ctx context.Context, client *openrelik.Client, out io.Writer, id int, limit int64, tty bool) error {
	body, _, err := client.Files().Download(ctx, id)
	if err != nil {
		return err
	}
	defer body.Close()

	var r io.Reader = body
	if limit >= 0 {
		r = io.LimitReader(body, limit)
	}
	if tty {
		br := bufio.NewReaderSize(r, binaryPeekSize)
		peek, err := br.Peek(binaryPeekSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if bytes.IndexByte(peek, 0) >= 0 {
			hint := fmt.Sprintf("pipe it (e.g. | xxd | head) or use 'openrelik download %d'", id)
			if file, _, err := client.Files().Info(ctx, id); err == nil {
				return fmt.Errorf("%q looks binary; %s", file.DisplayName, hint)
			}
			return fmt.Errorf("looks binary; %s", hint)
		}
		r = br
	}

	// A closed pipe, such as from '| head', is not an error.
	if _, err := io.Copy(out, r); err != nil && !errors.Is(err, syscall.EPIPE) {
		return err
	}
	return nil
}
