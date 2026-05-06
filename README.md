# OpenRelik CLI

A command-line interface for the OpenRelik API. It provides direct management of authentication, files, folders, and workflow execution from the terminal.

## Features

- **Resource Management:** Create and list folders, upload files, and mirror local directories directly into OpenRelik.
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

List existing folders or mirror a local directory to the server:

```bash
openrelik folder list
openrelik folder mirror /path/to/local/evidence
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
openrelik folder list --format json | jq '.[].id'
```
