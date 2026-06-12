#!/usr/bin/env bash
# scripts/release.sh
# One-click release for tmux-cli.
#
# Computes the next semver from the latest git tag, runs the test gate, then
# creates and pushes an annotated `vX.Y.Z` tag. The push triggers the Release
# workflow (.github/workflows/release.yml), which builds the binaries (with the
# version baked in via -ldflags), uploads them to the server, and publishes a
# GitHub release.
#
# Usage:
#   scripts/release.sh                 # patch bump (0.1.0 -> 0.1.1)
#   scripts/release.sh --minor         # 0.1.3 -> 0.2.0
#   scripts/release.sh --major         # 0.2.5 -> 1.0.0
#   scripts/release.sh --dry-run       # show what would happen, change nothing
#   scripts/release.sh --watch         # after pushing, stream the CI run
set -euo pipefail

BUMP="patch"
DRY_RUN=0
WATCH=0
for arg in "$@"; do
  case "$arg" in
    --patch)   BUMP="patch" ;;
    --minor)   BUMP="minor" ;;
    --major)   BUMP="major" ;;
    --dry-run) DRY_RUN=1 ;;
    --watch)   WATCH=1 ;;
    -h|--help) sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "Unknown argument: $arg (try --help)" >&2; exit 1 ;;
  esac
done

die() { echo "release: $1" >&2; exit 1; }

cd "$(git rev-parse --show-toplevel)" || die "not in a git repository"

# --- Pre-flight ----------------------------------------------------------------
branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$branch" = "main" ] || die "must release from 'main' (on '$branch')"

# Block on uncommitted *tracked* changes only; untracked working files (local
# notes, scratch binaries) don't affect what CI builds from the tag.
[ -z "$(git status --porcelain --untracked-files=no)" ] \
  || die "tracked files have uncommitted changes; commit or stash first"

echo "Fetching latest refs and tags..."
git fetch --quiet origin main --tags

# HEAD must match origin/main exactly (not ahead, not behind).
local_head="$(git rev-parse @)"
remote_head="$(git rev-parse @{u})"
[ "$local_head" = "$remote_head" ] || die "HEAD differs from origin/main; push/pull first"

# --- Compute next version ------------------------------------------------------
latest="$(git tag -l 'v*' | sort -V | tail -1)"
if [ -z "$latest" ]; then
  # No tags yet. 0.1.0 is the baseline that already shipped, so the first
  # tagged release bumps from it.
  latest="v0.1.0"
  echo "No existing tags; baselining at ${latest}."
fi

ver="${latest#v}"
IFS='.' read -r MAJOR MINOR PATCH <<< "$ver"
[[ "$MAJOR" =~ ^[0-9]+$ && "$MINOR" =~ ^[0-9]+$ && "$PATCH" =~ ^[0-9]+$ ]] \
  || die "latest tag '$latest' is not a clean vMAJOR.MINOR.PATCH"

case "$BUMP" in
  patch) PATCH=$((PATCH + 1)) ;;
  minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
  major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
esac
NEW="v${MAJOR}.${MINOR}.${PATCH}"

git rev-parse -q --verify "refs/tags/$NEW" >/dev/null && die "tag $NEW already exists"

echo "Current: ${latest}    Next: ${NEW}  (${BUMP} bump)"

# --- Test gate -----------------------------------------------------------------
echo "Running test gate: go test -race -short ./..."
if [ "$DRY_RUN" -eq 1 ]; then
  echo "[dry-run] would run tests, then tag and push ${NEW}"
  exit 0
fi
go test -race -short ./... || die "tests failed; not releasing"

# --- Tag & push ----------------------------------------------------------------
git tag -a "$NEW" -m "Release ${NEW}"
git push origin "$NEW"
echo "Pushed ${NEW}. Release workflow is now building and deploying."

repo="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)"
[ -n "$repo" ] && echo "Actions: https://github.com/${repo}/actions"

if [ "$WATCH" -eq 1 ] && command -v gh >/dev/null; then
  echo "Waiting for the run to register..."
  sleep 5
  gh run watch "$(gh run list --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')" --exit-status
fi
