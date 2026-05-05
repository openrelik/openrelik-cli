#!/usr/bin/env bash
set -euo pipefail

BUMP="${1:-minor}"  # minor | patch

LATEST=$(gh release view --json tagName --jq '.tagName' 2>/dev/null || echo "v0.0.0")
LATEST="${LATEST#v}"
IFS='.' read -r MAJOR MINOR PATCH <<< "$LATEST"

case "$BUMP" in
  patch)  VERSION="${MAJOR}.${MINOR}.$((PATCH + 1))" ;;
  minor)  VERSION="${MAJOR}.$((MINOR + 1)).0" ;;
  *)
    echo "Usage: $0 [minor|patch]"
    exit 1
    ;;
esac

TAG="v${VERSION}"

read -rp "Release ${TAG}? [Y/n] " CONFIRM
CONFIRM="${CONFIRM:-Y}"
[[ "$CONFIRM" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }

export GITHUB_TOKEN
GITHUB_TOKEN="$(gh auth token)"

git tag "${TAG}"
git push origin "${TAG}"
goreleaser release --clean

echo "Done: ${TAG}"
