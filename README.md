# OpenRelik CLI

A command-line interface for the OpenRelik API. It provides direct management of authentication, files, folders, and workflow execution from the terminal.

## Features

- **Resource Management:** Create and list folders, and upload files and local directories directly into OpenRelik.
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

Run a worker against an existing remote file by its ID, or provide a local file path to automatically upload and process it:

```bash
# Run 'strings' on file ID 123
openrelik run strings 123

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
openrelik folder list 42 --format json | jq '.files[].id'
```

`folder list --format json` returns an object, `{"folders": [...], "files": [...]}`. Older versions returned a plain array of folders, so update scripts that use `jq '.[].id'`.

## CLI Reference Guide

This section provides a detailed reference of the CLI commands and options.

### Global Flags

These flags modify output behavior for all commands:

*   `--format`: Set output styling to `human`, `json`, or `verbose`.
*   `--quiet`, `-q`: Suppress all output.

### Authentication (`auth`)

Configure credentials and active server connections. The CLI prioritizes `OPENRELIK_SERVER_URL` and `OPENRELIK_API_KEY` environment variables over local configuration settings.

*   `auth login`: Prompt for server URL and API key, saving them to `~/.openrelik/config.json`.
*   `auth status`: Show connection status and the active server.
*   `auth list`: List all configured servers.
*   `auth switch`: Change the active server.

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

*   `folder list [FOLDER]`: List root folders. If you provide a folder, list the subfolders and files inside it. `FOLDER` is a folder ID or a path of names from the root folders, such as `"Case A/evidence/"`; an argument of only digits is an ID, so add a trailing `/` to use it as a name. If several folders match a path, they are listed with their IDs so you can pick one. Folder names end in `/` and have TYPE `folder`; folder and file IDs are separate, so use folder IDs with `folder list` and file IDs with `file` commands. Entries are listed like `ls -latr`: sorted by last update, most recently updated last. Use `--sort name` to sort by name, `-r` to reverse the order, and `--limit N` to show only the N most recently updated.
*   `folder search <TERM>`: Search every folder you can access, including subfolders and folders shared with you, for names containing `TERM` (case-insensitive). Results show each folder's full path, which you can pass to `folder list` and the `--folder`/`--parent` flags. Shows the 30 most recently updated matches by default; use `--limit N` to show more or `--limit 0` for all. `--sort` and `-r` work as in `folder list`.
*   `folder tree [FOLDER]`: Show a folder's contents recursively as a tree, like `tree`, with each entry's ID in brackets. Without `FOLDER`, shows all root folders. Sorted by name; use `--sort time` and `-r` to change the order, `--level N` to limit the depth, `--folders-only` to leave out files, and `--size` to show file sizes.
*   `folder create <NAME>`: Create a new folder. Use `--parent <FOLDER>` (an ID or path) to create a subfolder.

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
```

### Uploading and Downloading (`upload`, `download`)

Move files between your machine and OpenRelik.

*   `upload <PATH> [PATH...]`: Upload files and directories. Directories are uploaded recursively (up to `--depth` levels, default 3) and recreated as folders. Use `--folder <FOLDER>` (an ID or path) to upload into an existing folder; otherwise a new root folder is created, named after the directory when uploading a single directory, or after the current time (e.g. `Upload 2026-10-27 14:05`). Use `--name` to choose the new folder's name.
*   `download --file <FILE_ID> [DESTINATION]`: Download a file. `DESTINATION` may be a file path or an existing directory; by default the file is saved in the current directory under its original name.

#### Examples

```bash
# Upload a disk image to folder 42, or to a folder by its path
openrelik upload /path/to/image.dd --folder 42
openrelik upload /path/to/image.dd --folder "Case A/evidence/"

# Upload a directory into a new root folder named "evidence"
openrelik upload /path/to/evidence

# Upload several files into a new root folder with a chosen name
openrelik upload disk.E01 memory.raw --name "Case 2026-001"

# Download file 105 into a directory
openrelik download --file 105 /tmp/downloads/
```

### File Management (`file`)

Inspect forensic files.

*   `file info <FILE_ID>`: Return file metadata including size, hashes, and MIME type.

### Worker Management (`worker`)

Inspect processing workers.

*   `worker list`: List registered workers and update the local cache at `~/.openrelik/workers_cache.json`.

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
*   `workflow run <ID>`: Start a created workflow.

#### Example

```bash
# Create and start a workflow from file 10 using template 5
openrelik workflow create --file 10 --template 5 --run
```

### Dynamic Worker Execution (`run`)

Run workers on files. The CLI reads the local worker cache to generate subcommands dynamically. Inputs can be server-side file IDs or local paths, which the CLI uploads automatically before running the worker.

*   `run <worker_name> <input>`: Run a specific worker on an input.
*   Use `--then` to chain workers sequentially (passing output from one to the next).
*   Use `--and` to run workers in parallel.
*   Use `--dry-run` to output the generated JSON workflow specification without executing it.

#### Examples

```bash
# Run the strings worker on file 100
openrelik run strings 100

# Run strings on a local file (uploads automatically)
openrelik run strings suspicious.exe

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

