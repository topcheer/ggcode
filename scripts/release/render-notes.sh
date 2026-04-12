#!/usr/bin/env bash
set -euo pipefail

tag="${1:?release tag is required}"
repo="${2:-${GITHUB_REPOSITORY:-topcheer/ggcode}}"
notes_file="docs/releases/${tag}.md"

if [[ -f "${notes_file}" ]]; then
  cat "${notes_file}"
  exit 0
fi

prev_tag="$(git tag --sort=-v:refname | grep -Fxv "${tag}" | head -n 1 || true)"
range="${tag}"
if [[ -n "${prev_tag}" ]]; then
  range="${prev_tag}..${tag}"
fi

echo "# ggcode ${tag}"
echo
echo "## Highlights"
echo
echo "- See the changelog below for the user-facing updates included in this release."
echo

if [[ -n "${prev_tag}" ]]; then
  echo "## Compare"
  echo
  echo "- Full diff: https://github.com/${repo}/compare/${prev_tag}...${tag}"
  echo
fi

echo "## Changelog"
echo
changelog="$(git log --no-merges --pretty='- %s' "${range}" | grep -Ev '^- (docs|ci|chore|style):' || true)"
if [[ -z "${changelog}" ]]; then
  echo "- No user-facing changes were captured in commit subjects for this release."
else
  printf '%s\n' "${changelog}"
fi
