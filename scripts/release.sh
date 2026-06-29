#!/usr/bin/env bash
# Cut a release in one step: bump the version everywhere it's pinned, commit,
# tag, and push. Pushing the tag triggers .github/workflows/release.yml, which
# builds the binaries, publishes the GitHub Release, and regenerates + pushes
# the Homebrew formula to claude-code-tools/homebrew-tap.
#
# The version lives in two managed places (kept in sync by this script):
#   - .claude-plugin/plugin.json   "version": "X.Y.Z"
#   - skills/mdview-review/SKILL.md  the vX.Y.Z binary pin (4 spots)
#
# Usage:
#   scripts/release.sh 0.1.3              bump, commit, tag, push (cuts a release)
#   scripts/release.sh 0.1.3 --no-push    bump, commit, tag locally only
set -euo pipefail

cd "$(dirname "$0")/.."

new="${1:-}"; new="${new#v}"          # accept "0.1.3" or "v0.1.3"
push=1; [ "${2:-}" = "--no-push" ] && push=0

if ! printf '%s' "$new" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "usage: $0 <major.minor.patch> [--no-push]   (got: '${1:-}')" >&2
  exit 1
fi

# Release from a clean main so unrelated changes don't ride along in the tag.
branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = "main" ] || { echo "refusing: on '$branch', not 'main'" >&2; exit 1; }
git diff --quiet && git diff --cached --quiet \
  || { echo "refusing: working tree not clean — commit or stash first" >&2; exit 1; }

old=$(sed -nE 's/.*"version"[[:space:]]*:[[:space:]]*"([0-9][^"]*)".*/\1/p' \
        .claude-plugin/plugin.json | head -1)
[ -n "$old" ] || { echo "could not read current version from plugin.json" >&2; exit 1; }
[ "$old" != "$new" ] || { echo "version is already $new" >&2; exit 1; }

echo "bumping $old -> $new"

# Portable in-place edit (BSD/macOS + GNU sed): write to temp, move back.
sub() { local f=$1 find=$2 repl=$3 tmp; tmp=$(mktemp); \
        sed "s|$find|$repl|g" "$f" > "$tmp" && mv "$tmp" "$f"; }

sub .claude-plugin/plugin.json "\"version\": \"$old\"" "\"version\": \"$new\""
sub skills/mdview-review/SKILL.md "v$old" "v$new"

# Guard: the old version must be fully gone from the files we manage.
old_re=${old//./\\.}
if grep -RnE "$old_re" .claude-plugin/plugin.json skills/mdview-review/SKILL.md; then
  echo "stale references to $old remain (see above) — aborting before commit" >&2
  exit 1
fi

git add .claude-plugin/plugin.json skills/mdview-review/SKILL.md
git commit -q -m "release: v$new"
git tag "v$new"
echo "committed + tagged v$new"

if [ "$push" = 1 ]; then
  git push -q && git push -q origin "v$new"
  echo "pushed main + tag v$new — the release workflow will build, publish, and update the formula"
  echo "watch it: gh run watch \$(gh run list --workflow=release.yml -L1 --json databaseId --jq '.[0].databaseId')"
else
  echo "--no-push: when ready, run  git push && git push origin v$new"
fi
