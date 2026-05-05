# Releasing

Releases are built with [GoReleaser](https://goreleaser.com) and published to
[openrelik/openrelik-cli](https://github.com/openrelik/openrelik-cli/releases).
A Homebrew cask is automatically pushed to
[openrelik/homebrew-openrelik-cli](https://github.com/openrelik/homebrew-openrelik-cli)
so users can install via `brew install --cask openrelik-cli`.

## Prerequisites

- [GoReleaser](https://goreleaser.com/install/) installed
- [GitHub CLI](https://cli.github.com) installed and authenticated (`gh auth login`)

## Using release.sh (recommended)

```bash
# Bump minor version (e.g. 0.1.0 → 0.2.0)
./release.sh

# Bump patch version (e.g. 0.1.0 → 0.1.1)
./release.sh patch
```

The script will:
1. Fetch the latest release tag from GitHub
2. Compute the next version and prompt for confirmation
3. Create and push the git tag
4. Run `goreleaser release --clean`

## Manual release

```bash
# 1. Ensure the working tree is clean
git status

# 2. Tag the release
git tag v0.2.0
git push origin v0.2.0

# 3. Run GoReleaser
export GITHUB_TOKEN=$(gh auth token)
goreleaser release --clean
```

GoReleaser will:
- Build binaries for linux/darwin/windows (amd64 + arm64)
- Create a GitHub Release with tarballs, zips, and `checksums.txt`
- Push `Casks/openrelik-cli.rb` to `openrelik/homebrew-openrelik-cli`

## Local build (no publish)

```bash
goreleaser build --snapshot --clean
```

Binaries are written to `dist/`. No tag required, nothing is published.

## User installation

```bash
brew tap openrelik/openrelik-cli
brew install --cask openrelik-cli
```

## User upgrade

```bash
brew update && brew upgrade --cask openrelik-cli
```

## User uninstall

```bash
brew uninstall --cask openrelik-cli
brew untap openrelik/openrelik-cli  # optional, removes the tap
```
