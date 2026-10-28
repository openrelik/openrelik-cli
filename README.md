# OpenRelik CLI

A command-line interface for the OpenRelik API. It provides direct management of authentication, files, folders, and workflow execution from the terminal.

## Features

- **Resource Management:** Create and list folders, and upload files and local directories directly into OpenRelik.
- **Sharing:** Share folders with users and groups as viewer, editor or owner, and see who has access.
- **Workflow Execution:** Execute tasks on remote files or local paths (e.g., string extraction, grepping, malware analysis). Subcommands are dynamically generated from registered workers.
- **Execution Topologies:** Chain workers sequentially (`--then`) or execute them in parallel (`--and`).
- **Data Formatting:** Output responses in human-readable tables, verbose structures, or JSON for integration into external pipelines.

## Examples

### Authentication

Authenticate with the OpenRelik server interactively:

```bash
openrelik auth login
```

Alternatively, set the `OPENRELIK_SERVER_URL` and `OPENRELIK_API_KEY` environment variables.

### Folder Management

List existing folders or upload a local directory to the server:

```bash
openrelik folder list
openrelik upload /path/to/local/evidence
```

### Worker Execution

Run a worker against a file on the server, by its ID or path, or provide a local file path to automatically upload and process it:

```bash
# Run 'strings' on file ID 123
openrelik run strings 123

# Run 'strings' on a file on the server, by path
openrelik run strings "Case A/evidence/disk.E01"

# Run 'strings' on a local file
openrelik run strings suspicious_file.bin
```

### Complex Workflows

Construct workflows using sequential or parallel topologies.

**Sequential (Output routing):**
Pass the output of the first worker as the input to the second worker.

```bash
openrelik run strings --then grep --regex "password" 123
```

**Parallel (Shared input):**
Execute multiple workers concurrently against the same initial input.

```bash
openrelik run strings --and grep --regex "password" 123
```

### Scripting and Automation

Enforce JSON output to integrate with command-line JSON processors like `jq`:

```bash
openrelik folder list --format json | jq '.folders[].id'

# Get the ID of "Case 7", creating the folder only if it doesn't exist yet
openrelik folder create "Case 7" --exist-ok --format json | jq .id
openrelik folder list 42 --format json | jq '.files[].id'
```

`folder list --format json` returns an object, `{"folders": [...], "files": [...]}`. Older versions returned a plain array of folders, so update scripts that use `jq '.[].id'`.

The human-readable `folder list` table now has the columns `ID UPDATED SIZE TYPE DISPLAY NAME`, with the name last so rows line up while they are printed. Older versions put `DISPLAY NAME` second. Scripts should use `--format json`, which is not affected.

## CLI Reference Guide

This section provides a detailed reference of the CLI commands and options.

### Global Flags

These flags apply to all commands:

*   `--format`: Set output styling to `human`, `json`, or `verbose`. Any other value is an error.
*   `--quiet`, `-q`: Suppress all output.
*   `--server`: Use this server instead of the active one, with the API key saved for it (or `OPENRELIK_API_KEY`).
*   `--debug`: Show errors from the server as they were received, with the request that failed, instead of a plain description.

Ctrl-C stops a command cleanly (a second Ctrl-C ends it at once). Interrupting `run` doesn't stop the workflow on the server; the message says how to follow it.

### Exit Status

| Status | Meaning |
|---|---|
| 0 | Success |
| 1 | Any other error, including a workflow that failed |
| 2 | The command line is wrong: an unknown command or flag, missing or extra arguments, or an invalid value |
| 3 | A file, folder, workflow or other resource doesn't exist |
| 4 | No server or API key is set, the server didn't accept the API key, or permission was denied |
| 5 | The server can't be reached or failed to handle the request; worth retrying |
| 130 | Interrupted with Ctrl-C |

Progress bars and colours are only shown when stderr is a terminal. Set `NO_COLOR` to turn colours off; with `TERM=dumb`, progress is written as one line per transfer.

### Authentication (`auth`)

Configure credentials and active server connections. The server is taken from `--server`, then `OPENRELIK_SERVER_URL`, then the active server. The API key is taken from `OPENRELIK_API_KEY`, then the key saved for that server. There is no flag for the API key, since command lines show up in process lists and shell history.

*   `auth login [SERVER]`: Check an API key with the server and save it to `~/.openrelik/`. Prompts for anything missing; use `--api-key-stdin` to pipe the key in from a script.
*   `auth status`: Show which server and API key commands use, and check the login.
*   `auth list`: List the saved servers.
*   `auth switch [SERVER]`: Change the active server, by URL or by number from `auth list`.
*   `auth logout [SERVER]`: Remove a saved server and its API key (the active server by default).

#### Examples

```bash
# Start interactive login
openrelik auth login

# Run commands without storing credentials locally
export OPENRELIK_SERVER_URL=http://localhost:8710
export OPENRELIK_API_KEY=your-api-key
```

### User Profiles (`user`)

Inspect user account details.

*   `user me`: Show metadata for the currently authenticated user profile.

#### Example

```bash
openrelik user me --format json
```

### Folder Management (`folder`)

Organize files in hierarchical directories.

*   `folder list [FOLDER]`: List root folders. If you provide a folder, list the subfolders and files inside it. `FOLDER` is a folder ID or a path of names from the root folders, such as `"Case A/evidence/"` (a leading `/`, as `folder search` prints paths, is optional); an argument of only digits is an ID, so add a trailing `/` to use it as a name. Folder names may contain `/`, so `foo/bar/123` also finds a folder named `foo/bar/123`. If several folders match a path, they are listed with their IDs so you can pick one. Folder names end in `/` and have TYPE `folder`; folder and file IDs are separate, so use folder IDs with `folder list` and file IDs with `file` commands. Entries are listed like `ls -latr`: sorted by last update, most recently updated last. Use `--sort name` to sort by name, `-r` to reverse the order, and `--limit N` to show only the N most recently updated. The root listing includes folders other users shared with you; `--shared` lists only those, with their owners. On servers that sort root folders, the root listing is printed as it arrives, and `--limit` up to 1000 only fetches the folders shown. Otherwise (older servers, `-r`, `--sort name`, larger limits, or JSON output) all root folders are fetched first, with a count on stderr while that runs. If printing stops partway, for example when the connection drops, the error says how many folders were printed.
*   `folder search <TERM>`: Search every folder you can access, including subfolders and folders shared with you, for names containing `TERM` (case-insensitive). Results show each folder's full path, which you can pass to `folder list`, `upload` and `folder create --parent`. All matches are shown; use `--limit N` to show only the N most recently updated. `--sort` and `-r` work as in `folder list`. Use `--latest` to show only the most recently updated match, and `--root` to search only root folders (and folders shared with you).
*   `folder tree [FOLDER]`: Show a folder's contents recursively as a tree, like `tree`, with each entry's ID in brackets. Without `FOLDER`, shows all root folders. Sorted by name like `tree`, so the structure is easy to scan (`folder list` and `folder search` sort by last update like `ls -latr`, so the latest work is shown last); use `--sort time` and `-r` to change the order, `--level N` to limit the depth, `--folders-only` to leave out files, and `--size` to show file sizes.
*   `folder create <NAME>`: Create a new folder. Use `--parent <FOLDER>` (an ID or path) to create a subfolder. Use `--exist-ok` to print an existing folder with the same name in that location instead of creating a duplicate, which makes scripts safe to re-run. If several such folders exist it fails and lists them; add `--latest` to use the most recently updated one.
*   `folder share <FOLDER>`: Give users (`--user NAME`) and groups (`--group NAME`) a role on a folder with `--role viewer|editor|owner` (required). Both flags can be repeated. Sharing a folder gives access to all its files and subfolders; files can't be shared on their own. A user or group that already has a role ends up with only the new one (the new role is added before the old one is removed). Each user and group is reported; if any fail, the rest are still shared and the command exits non-zero. Changes that would leave the folder without an owner are refused, and lowering your own role asks first (`--force` skips the question).
*   `folder unshare <FOLDER>`: Remove every role the given `--user` and `--group` have on the folder. Roles on parent folders still give access; the command warns about them. Removing your own role asks first (`--force` skips the question).
*   `folder access <FOLDER>`: Show your role on a folder, and the users and groups with a role on it or on its parent folders, with the folder each role is set on.

#### Examples

```bash
# Create a root folder
openrelik folder create "Investigation 2026"

# Create a subfolder under folder ID 12
openrelik folder create "Evidence" --parent 12

# Create a subfolder by parent path
openrelik folder create "logs" --parent "Investigation 2026/Evidence/"

# List the subfolders and files in folder 12
openrelik folder list 12

# List a folder by its path from the root folders
openrelik folder list "Case A/evidence/"

# Find folders with "plaso" in their name
openrelik folder search plaso

# Show everything below a folder as a tree
openrelik folder tree "Case A/"

# Let alice view a case folder, and give the IR team edit access
openrelik folder share "Case A/" --user alice --role viewer
openrelik folder share "Case A/" --group ir-team --role editor

# See who can access it, then remove alice's access
openrelik folder access "Case A/"
openrelik folder unshare "Case A/" --user alice
```

### Uploading and Downloading (`upload`, `download`)

Move files between your machine and OpenRelik.

*   `upload <PATH>... [FOLDER]`: Upload files and directories. With more than one argument, the last one is the existing folder to upload into, given as an ID or path (like `cp`). Directories are uploaded recursively and recreated as subfolders; use `--depth N` to upload only N levels, and the directories left out are listed. With a single argument, or with `/` as `FOLDER`, a new root folder is created instead: a single directory becomes the root folder itself, and anything else goes into a folder named after the current time in UTC (e.g. `Upload 2026-10-27 14:05 UTC`). Use `--name` to choose the new folder's name. Files given directly are skipped if `FOLDER` already holds a file with the same name and SHA256, so an interrupted upload can simply be run again; if the server has no hash for a file with the same name and size (files over 5 GB aren't hashed), you are asked whether to upload it, and without a terminal the upload fails instead. Use `--force` to upload without checking.
*   `download <FILE> [DESTINATION]`: Download a file. `FILE` is a file ID or a path of folder names ending in the file name, as for `file cat`. `DESTINATION` may be a file path or an existing directory; by default the file is saved in the current directory under its original name.

#### Examples

```bash
# Upload a disk image to folder 42, or to a folder by its path
openrelik upload /path/to/image.dd 42
openrelik upload /path/to/image.dd "Case A/evidence/"

# Upload a directory into a new root folder named "evidence"
openrelik upload /path/to/evidence

# Upload several files into a new root folder with a chosen name
openrelik upload disk.E01 memory.raw / --name "Case 2026-001"

# Download file 105 into a directory, or a file by its path
openrelik download 105 /tmp/downloads/
openrelik download "Case A/evidence/notes.txt"
```

### File Management (`file`)

Inspect and print forensic files.

*   `file info <FILE>`: Return file metadata including size, hashes, and MIME type. `FILE` is a file ID or a path, as for `file cat`.
*   `file cat <FILE> [FILE...]`: Print file contents to stdout. FILE is a file ID or a path of folder names ending in the file name, such as `"Case A/evidence/notes.txt"`. File names may contain `/`, so `Case A/2024/report.txt` also finds a file named `2024/report.txt` in `Case A`. Several files are concatenated in the order given. Use `-n` to number the lines, like `cat -n`, and `-c N` (e.g. `512`, `4K`, `10M`) to print only the first N bytes of each file; the download stops once they have been read. Files that look binary are refused when stdout is a terminal; pipe or redirect the output to print them anyway. Every part of a path is a name, so `42/notes.txt` looks in a root folder named `42`, not folder 42; use the file ID instead.

#### Example

```bash
openrelik file cat 4070 | grep -i password
openrelik file cat "Case A/logs/syslog" | head -50
openrelik file cat -c 512 "Case A/evidence/disk.E01" | xxd
```

### Worker Management (`worker`)

Inspect processing workers.

*   `worker list`: List registered workers, with the `run` subcommand for each, and update the local cache at `~/.openrelik/workers_cache.json`.

#### Example

```bash
openrelik worker list
```

### Templates and Workflows (`template`, `workflow`)

Manage and execute forensic pipelines.

*   `template list`: List available workflow templates.
*   `workflow create`: Create a workflow. Requires `--file <ID>` and `--template <ID>`. Use `--run` to execute immediately.
*   `workflow info <ID>`: Display workflow structure and configuration.
*   `workflow status <ID>`: Show current execution status.
*   `workflow wait <ID>`: Wait for a workflow to finish and show its status. Exits 1 if it failed; `--timeout 30m` stops waiting after a while.
*   `workflow run <ID>`: Start a created workflow.

#### Example

```bash
# Create and start a workflow from file 10 using template 5
openrelik workflow create --file 10 --template 5 --run

# Wait for workflow 99, then download its results if it succeeded
openrelik workflow wait 99 && openrelik workflow download 99
```

### Dynamic Worker Execution (`run`)

Run workers on files. The CLI generates a subcommand for each worker from the local worker cache, which is updated when a workflow starts, when `run --help` is shown, or with `worker list`. When a workflow finishes, the output files of its final tasks are downloaded: the end of a chain, or each worker run with `--and`. Inputs can be file IDs, paths of files on the server (such as `"Case A/evidence/disk.E01"`, as for `file info`), or local paths, which the CLI uploads automatically before running the worker. A local file isn't uploaded again if the upload folder already holds a file with the same name and SHA256; that file is used instead. An input that isn't a local file and contains a `/` is a server path. If a relative path names both a local file and a file on the server, `run` fails rather than guess: write `./PATH` for the local file, or use the file ID.

*   `run <worker_name> <input>`: Run a specific worker on an input.
*   Use `--then` to chain workers sequentially (passing output from one to the next).
*   Use `--and` to run workers in parallel.
*   Use `--dry-run` to output the generated JSON workflow specification without executing it.
*   Use `--download final|all|none` to choose which results are downloaded (default `final`), and `--download-result` or `--no-download-result` after a worker to override that for the worker's task. `--no-download` was removed; use `--download none`.

#### Examples

```bash
# Run the strings worker on file 100
openrelik run strings 100

# Run strings on a local file (uploads automatically)
openrelik run strings suspicious.exe

# Run strings on a file on the server, by path
openrelik run strings "Case A/evidence/disk.E01"

# Chain strings to grep
openrelik run strings --then grep --regex "password" 100

# Preview the generated workflow JSON spec without running
openrelik run strings --then grep --regex "password" 100 --dry-run
```

### AI Agent Skill Integration (`skill`)

Install specification files for AI assistants.

*   `skill install [dir]`: Query registered workers dynamically from the active OpenRelik server and generate a customized `SKILL.md` file. This tailors the skill capabilities directly to your server's available workers so AI agents can discover and run them.
*   Use `--harness` to target frameworks (`generic`, `codex`, `gemini`, `claude`, `opencode`, `pi`, `copilot`).
*   Use `--local` to install the skill in the current project directory.

#### Examples

```bash
# Install the skill to the default global directory for Gemini (~/.gemini/skills/openrelik/SKILL.md)
openrelik skill install --harness gemini

# Install the skill locally in the current folder structure
openrelik skill install --local --harness generic
```

