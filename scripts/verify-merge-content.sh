#!/usr/bin/env bash
# verify-merge-content.sh - post-merge content assertion (#5251, item 1).
#
# Adjacent same-file PRs can merge "successfully" while a later merge
# silently overwrites an earlier one's content (#5246/#5247 live case):
# GitHub shows MERGED+CLOSED but the signature lines are missing from
# main until a periodic grep catches it. This helper closes that window:
# run it immediately after merging a PR, asserting its signature lines
# are present on HEAD.
#
# Usage:
#   scripts/verify-merge-content.sh <file>:<grep-pattern> [file:pattern ...]
#
#   scripts/verify-merge-content.sh \
#     internal/agent/verify_hint.go:'func mutatingSourceEdit' \
#     internal/agent/unread_edit_guard.go:'case "apply_patch"'
#
# Exit 0 = every pattern found on HEAD; exit 1 = list of missing pins.
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "usage: $0 <file>:<grep-pattern> [file:pattern ...]" >&2
  exit 2
fi

missing=()
for spec in "$@"; do
  file="${spec%%:*}"
  pattern="${spec#*:}"
  if [[ "$file" == "$pattern" ]]; then
    echo "error: spec '$spec' must be <file>:<grep-pattern>" >&2
    exit 2
  fi
  if ! grep -qE -- "$pattern" "$file" 2>/dev/null; then
    missing+=("$spec")
  fi
done

if (( ${#missing[@]} > 0 )); then
  echo "POST-MERGE CONTENT ASSERTION FAILED - signature lines missing from HEAD:" >&2
  for spec in "${missing[@]}"; do
    echo "  $spec" >&2
  done
  echo "A later merge may have overwritten this PR's content (see #5251/#5246)." >&2
  exit 1
fi

echo "post-merge content assertion passed (${#@} pin(s) present on HEAD)."
