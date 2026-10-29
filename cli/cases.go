// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/openrelik/openrelik-cli/util"
	openrelik "github.com/openrelik/openrelik-go-client"
	"github.com/spf13/cobra"
)

// A case folder is the one root folder of a case, found by its case
// reference. With --case, commands work inside the case folder: folder
// arguments are resolved from it, and IDs must be inside it, so data can't
// end up outside the case by mistake.

// errCaseFoldersUnsupported is returned when the server is too old for case
// folders.
var errCaseFoldersUnsupported = errors.New("this server doesn't support case folders")

// caseError turns the error of a case folder request into one that says
// what went wrong for caseRef: the server doesn't support case folders
// (exit 1), the case doesn't exist or isn't visible (exit 3), or the
// reference is invalid (exit 2). Other errors are returned as they are.
func caseError(err error, caseRef string) error {
	if errors.Is(err, openrelik.ErrCaseFoldersUnsupported) {
		return errCaseFoldersUnsupported
	}
	var apiErr *openrelik.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return notFoundError(fmt.Sprintf("case %s not found, or you don't have access to it", caseRef))
		case http.StatusUnprocessableEntity:
			return usageErrorf("invalid case reference %q: %s", caseRef, apiErr.Message)
		}
	}
	return err
}

// checkCaseRef fails with a usage error for references the client refuses
// to send, so that its other errors keep their exit codes. The server
// checks the rest.
func checkCaseRef(caseRef string) error {
	if strings.TrimSpace(caseRef) == "" {
		return usageErrorf("case reference must not be empty")
	}
	for _, part := range strings.Split(caseRef, "/") {
		if part == "" || part == "." || part == ".." {
			return usageErrorf("invalid case reference %q: it must not contain '//', start or end with '/', or have '.' or '..' between slashes", caseRef)
		}
	}
	return nil
}

// lookupCase returns the case folder of caseRef. If someone else owns it,
// it says so on stderr, so the user knows whose folder the data goes into.
func lookupCase(cmd *cobra.Command, client *openrelik.Client, caseRef string) (*openrelik.Folder, error) {
	if err := checkCaseRef(caseRef); err != nil {
		return nil, err
	}
	folder, _, err := client.Folders().GetCaseFolder(cmd.Context(), caseRef)
	if err != nil {
		return nil, caseError(err, caseRef)
	}
	noteCaseOwner(cmd, client, folder, "Using case folder")
	return folder, nil
}

// noteCaseOwner writes "<prefix> 42 "ACME", owned by alice." to stderr if
// the folder belongs to someone other than the user.
func noteCaseOwner(cmd *cobra.Command, client *openrelik.Client, folder *openrelik.Folder, prefix string) {
	if quiet || outputFormat != "human" {
		return
	}
	me, _, err := client.Users().GetMe(cmd.Context())
	if err != nil || me.ID == folder.User.ID {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %d %q, owned by %s.\n", prefix, folder.ID, folder.DisplayName, folder.User.Username)
}

// resolveFolderIn resolves a folder argument like resolveFolder, but inside
// caseFolder if it isn't nil: paths start at its subfolders, and an ID must
// be the case folder or a folder below it.
func resolveFolderIn(ctx context.Context, client *openrelik.Client, caseFolder *openrelik.Folder, arg string) (int, error) {
	if caseFolder == nil {
		return resolveFolder(ctx, client, arg)
	}
	if !folderIDPattern.MatchString(arg) {
		return resolveFolderPath(ctx, client, caseFolder, arg)
	}
	id, err := resolveFolder(ctx, client, arg)
	if err != nil {
		return 0, err
	}
	in, err := inCase(ctx, client, caseFolder, id)
	if err != nil {
		return 0, err
	}
	if !in {
		return 0, usageErrorf("folder %d is not in case %s (folder %d %q)", id, caseFolder.CaseRef, caseFolder.ID, caseFolder.DisplayName)
	}
	return id, nil
}

// resolveFileIn resolves a file argument like resolveFile, but inside
// caseFolder if it isn't nil: paths start at its subfolders, and an ID must
// be a file in the case folder or a folder below it.
func resolveFileIn(ctx context.Context, client *openrelik.Client, caseFolder *openrelik.Folder, arg string) (int, error) {
	if caseFolder == nil {
		return resolveFile(ctx, client, arg)
	}
	if !folderIDPattern.MatchString(arg) {
		m, err := resolveFilePath(ctx, client, caseFolder, arg)
		if err != nil {
			return 0, err
		}
		return m.file.ID, nil
	}
	id, err := resolveFile(ctx, client, arg)
	if err != nil {
		return 0, err
	}
	return id, checkFileInCase(ctx, client, caseFolder, id)
}

// checkFileInCase fails if file id isn't in caseFolder or a folder below it.
func checkFileInCase(ctx context.Context, client *openrelik.Client, caseFolder *openrelik.Folder, id int) error {
	file, _, err := client.Files().Info(ctx, id)
	if err != nil {
		return err
	}
	in, err := inCase(ctx, client, caseFolder, file.Folder.ID)
	if err != nil {
		return err
	}
	if !in {
		return usageErrorf("file %d is not in case %s (folder %d %q)", id, caseFolder.CaseRef, caseFolder.ID, caseFolder.DisplayName)
	}
	return nil
}

// inCase reports whether folderID is the case folder or a folder below it.
func inCase(ctx context.Context, client *openrelik.Client, caseFolder *openrelik.Folder, folderID int) (bool, error) {
	if folderID == caseFolder.ID {
		return true, nil
	}
	folder, _, err := client.Folders().Get(ctx, folderID)
	if err != nil {
		return false, err
	}
	for f := folder.Parent; f != nil; f = f.Parent {
		if f.ID == caseFolder.ID {
			return true, nil
		}
	}
	return false, nil
}

// isCaseNotFound reports whether err is the 404 of a server with case
// folders for a case that doesn't exist or isn't visible.
func isCaseNotFound(err error) bool {
	var apiErr *openrelik.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound &&
		!errors.Is(err, openrelik.ErrCaseFoldersUnsupported)
}

// isConflict reports whether err is a 409 Conflict, as for a taken case
// reference.
func isConflict(err error) bool {
	var apiErr *openrelik.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

func newCaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "case",
		Aliases: []string{"cases"},
		Short:   "Manage case folders",
		Long: `Create, list and mark case folders.

A case folder is the one root folder of a case, found by its case
reference, such as case-123, instead of by its name, so everyone's data for
the case ends up in the same place. Each case has at most one case folder
on the server.

To work inside a case, add --case REF to 'folder list', 'folder tree',
'folder create', 'upload', 'run' and 'workflow list'.

The server stores references in lower case, with spaces replaced by '-',
so "CASE 123" and "case-123" are the same case. References can contain
letters, digits, '-', '_', '.' and '/', and start and end with a letter or
digit.`,
	}

	cmd.AddCommand(newCaseCreateCmd())
	cmd.AddCommand(newCaseListCmd())
	cmd.AddCommand(newCaseSetCmd())
	cmd.AddCommand(newCaseClearCmd())
	return cmd
}

func newCaseCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <REF> [NAME]",
		Short: "Create the case folder of a case",
		Long: `Create the case folder of case REF: a new root folder named NAME, or REF
if NAME isn't given.

If the case folder already exists and you can access it, it is printed
and the command succeeds, so it is safe to run again. The folder keeps its
name. If it exists but you can't access it, the command fails: ask your
OpenRelik admin for access.

To make an existing root folder the case folder, use 'case set'.`,
		Example: `  # Create the case folder of case-123
  openrelik case create case-123 "ACME ransomware"

  # Get the ID of the case folder, creating it if needed
  openrelik case create case-123 --format json | jq .id`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, name := args[0], args[0]
			if len(args) > 1 {
				name = args[1]
			}
			if err := checkCaseRef(ref); err != nil {
				return err
			}
			if strings.TrimSpace(name) == "" {
				return usageErrorf("folder name must not be empty")
			}

			client, err := newClient()
			if err != nil {
				return err
			}

			// Look first: on servers without case folders, creating would
			// make an ordinary root folder.
			existing, _, err := client.Folders().GetCaseFolder(cmd.Context(), ref)
			if err == nil {
				return printExistingCase(cmd, client, existing, name)
			}
			if !isCaseNotFound(err) {
				return caseError(err, ref)
			}

			folder, _, err := client.Folders().CreateCaseFolder(cmd.Context(), ref, name)
			switch {
			case errors.Is(err, openrelik.ErrCaseFoldersUnsupported) && folder != nil:
				return fmt.Errorf("%w; folder %d %q was created as an ordinary root folder", errCaseFoldersUnsupported, folder.ID, folder.DisplayName)
			case isConflict(err):
				// Someone created it since we looked. Use it if we can see it.
				if existing, _, getErr := client.Folders().GetCaseFolder(cmd.Context(), ref); getErr == nil {
					return printExistingCase(cmd, client, existing, name)
				}
				return err
			case err != nil:
				return caseError(err, ref)
			}
			return formatAndPrint(cmd, &FolderCreatedView{Folder: folder, Created: true})
		},
	}
	return cmd
}

// printExistingCase prints the case folder that 'case create' found, saying
// on stderr whose it is and that it keeps its name.
func printExistingCase(cmd *cobra.Command, client *openrelik.Client, folder *openrelik.Folder, name string) error {
	noteCaseOwner(cmd, client, folder, fmt.Sprintf("Case %s already exists: folder", folder.CaseRef))
	if name != folder.DisplayName && !quiet && outputFormat == "human" {
		fmt.Fprintf(cmd.ErrOrStderr(), "It keeps its name %q; it wasn't renamed to %q.\n", folder.DisplayName, name)
	}
	return formatAndPrint(cmd, &FolderCreatedView{Folder: folder, Created: false})
}

func newCaseListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the cases you can access",
		Long: `List the case folders you can access, your own and those shared with you,
sorted by case reference. Cases you can't access aren't listed.`,
		Example: `  # List the cases
  openrelik case list

  # Print the case references
  openrelik case list --format json | jq -r '.[].case_ref'`,
		Args: util.UseArgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			roots, err := listRootFolders(cmd, client)
			if err != nil {
				return err
			}
			var cases []openrelik.Folder
			for _, f := range roots {
				if f.CaseRef != "" {
					cases = append(cases, f)
				}
			}
			if len(cases) == 0 {
				// Older servers send no case references at all; tell that
				// apart from having no cases.
				_, _, err := client.Folders().GetCaseFolder(cmd.Context(), "case")
				if errors.Is(err, openrelik.ErrCaseFoldersUnsupported) {
					return errCaseFoldersUnsupported
				}
			}
			sort.Slice(cases, func(i, j int) bool { return cases[i].CaseRef < cases[j].CaseRef })
			return formatAndPrint(cmd, &CaseListView{Cases: cases})
		},
	}
	return cmd
}

func newCaseSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <FOLDER> <REF>",
		Short: "Make a root folder the case folder of a case",
		Long: `Make FOLDER the case folder of case REF, or change its case reference.
FOLDER is a folder ID or a path of folder names, as accepted by 'folder
list'. Only root folders can be case folders, and each case has at most
one, so this fails if another folder is the case folder of REF.

Editors and owners of the folder can set its case reference, and so can
admins, on any folder.`,
		Example: `  # Make root folder 42 the case folder of case-123
  openrelik case set 42 case-123`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateCaseRef(cmd, args[0], args[1])
		},
	}
	return cmd
}

func newCaseClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear <FOLDER>",
		Short: "Remove the case reference of a folder",
		Long: `Remove the case reference of FOLDER. The folder stays, as an ordinary
root folder, and the reference is free for another folder. FOLDER is a
folder ID or a path of folder names, as accepted by 'folder list'.

Editors and owners of the folder can remove its case reference, and so can
admins, on any folder.`,
		Example: `  # Make folder 42 an ordinary root folder again
  openrelik case clear 42`,
		Args: util.UseArgsWithQuoteHint(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateCaseRef(cmd, args[0], "")
		},
	}
	return cmd
}

// updateCaseRef sets the case reference of the folder arg to ref, or
// removes it if ref is empty, and prints the change.
func updateCaseRef(cmd *cobra.Command, arg, ref string) error {
	if ref != "" {
		if err := checkCaseRef(ref); err != nil {
			return err
		}
	}
	client, err := newClient()
	if err != nil {
		return err
	}

	id, err := resolveFolder(cmd.Context(), client, arg)
	if err != nil {
		return err
	}
	folder, _, err := client.Folders().Get(cmd.Context(), id)
	if err != nil {
		return err
	}
	view := &CaseRefUpdatedView{Previous: folder.CaseRef}

	if ref == "" {
		if _, err := client.Folders().ClearCaseRef(cmd.Context(), id); err != nil {
			return updateCaseError(err)
		}
		folder.CaseRef = ""
		view.Folder = folder
	} else {
		view.Folder, _, err = client.Folders().SetCaseRef(cmd.Context(), id, ref)
		if err != nil {
			return updateCaseError(err)
		}
	}
	return formatAndPrint(cmd, view)
}

// updateCaseError explains a failed 'case set' or 'case clear'. A 404 is
// about the folder, so it is left to friendlyErrors.
func updateCaseError(err error) error {
	if errors.Is(err, openrelik.ErrCaseFoldersUnsupported) {
		return errCaseFoldersUnsupported
	}
	var apiErr *openrelik.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
		return usageErrorf("%s", apiErr.Message)
	}
	return err
}
