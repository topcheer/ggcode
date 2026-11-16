#!/usr/bin/env bash
# stale-base-guard.sh - create/synchronize-time stale-base warning for PRs (#5270).
#
# Warns when a PR branch is far behind its base and/or carries commits whose
# content already reached the base branch under different SHAs (replayed
# work), so the pseudo-delta is flagged BEFORE review effort is spent.
#
# Two signals, sequenced per the issue thread (watchdog notes):
#   1. behind-count (cheap, necessary): git rev-list --count <head>..<base>.
#      Fires the warning alone when above BEHIND_THRESHOLD (default 3).
#   2. patch-id equivalence (confirming): git log --no-merges -p <range> |
#      git patch-id --stable over merge-base..base vs merge-base..head -
#      the same method as the internal/tool pr_delta tool. Only this signal
#      escalates the wording to "N commits appear already merged upstream;
#      rebase recommended".
#
# Modes:
#   comment mode - when GITHUB_TOKEN and PR_NUMBER are set: maintains ONE PR
#     comment tagged with the stale-base-guard marker: created on the first
#     warning, edited in place when the state changes, flipped to resolved
#     after a rebase clears both signals, and left silent when clean.
#   detect mode  - otherwise: prints the machine line and the comment body
#     to stdout (used by the regression test and manual runs).
#
# The guard NEVER blocks: every detection outcome exits 0; only usage errors
# exit non-zero (and the workflow runs this job with continue-on-error).

set -uo pipefail

BEHIND_THRESHOLD="${BEHIND_THRESHOLD:-3}"
BASE_REF="${BASE_REF:-}"
HEAD_REF="${HEAD_REF:-HEAD}"
PR_NUMBER="${PR_NUMBER:-}"
REPO="${REPO:-${GITHUB_REPOSITORY:-}}"
GITHUB_TOKEN="${GITHUB_TOKEN:-}"
MARKER='<!-- stale-base-guard -->'

log() { echo "stale-base-guard: $*"; }
die() { echo "stale-base-guard: $*" >&2; exit 2; }

command -v git >/dev/null 2>&1 || die "git not found"

base="$BASE_REF"
if [ -z "$base" ]; then
  base="$(git symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null || true)"
  [ -n "$base" ] || die "BASE_REF unset and origin/HEAD not set; pass BASE_REF explicitly"
fi
git rev-parse --verify --quiet "$base^{commit}" >/dev/null 2>&1 || die "base ref not found: $base"
git rev-parse --verify --quiet "$HEAD_REF^{commit}" >/dev/null 2>&1 || die "head ref not found: $HEAD_REF"
base_sha="$(git rev-parse "$base^{commit}")"
head_sha="$(git rev-parse "$HEAD_REF^{commit}")"

merge_base="$(git merge-base "$base_sha" "$head_sha" 2>/dev/null || true)"
[ -n "$merge_base" ] || die "no merge-base between $base and $HEAD_REF"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Signal 1: how far the branch is behind its base.
behind="$(git rev-list --count "$head_sha..$base_sha")"
total="$(git rev-list --count --no-merges "$merge_base..$head_sha")"

# Signal 2: patch-id equivalence. 'git patch-id --stable' prints one
# "<patch-id> <commit-sha>" line per non-merge commit with a non-empty diff.
git log --no-merges -p "$merge_base..$base_sha" | git patch-id --stable \
  | awk 'NF >= 1 { print $1 }' | sort -u >"$tmp/base-pids"
git log --no-merges -p "$merge_base..$head_sha" | git patch-id --stable \
  | awk 'NF >= 2 { print $1, $2 }' >"$tmp/pr-pids"

# PR commits whose patch-id also exists on the base side = replayed work.
awk 'NR == FNR { want[$1] = 1; next } ($1 in want) { print $2 }' \
  "$tmp/base-pids" "$tmp/pr-pids" >"$tmp/absorbed-shas"
absorbed="$(wc -l <"$tmp/absorbed-shas" | tr -d '[:space:]')"

absorbed_md="$tmp/absorbed.md"
: >"$absorbed_md"
while IFS= read -r sha; do
  [ -n "$sha" ] || continue
  git log -1 --format='- %h %s' "$sha" >>"$absorbed_md"
done <"$tmp/absorbed-shas"

verdict="clean"
[ "$behind" -gt "$BEHIND_THRESHOLD" ] && verdict="warn"
[ "$absorbed" -gt 0 ] && verdict="escalate"

log "behind=$behind pr_commits=$total absorbed=$absorbed threshold=$BEHIND_THRESHOLD verdict=$verdict merge_base=${merge_base:0:9}"

body="$tmp/body.md"
{
  echo "$MARKER"
  echo "## stale-base guard (#5270)"
  echo
  if [ "$verdict" = "escalate" ]; then
    echo "**$absorbed of $total commits appear already merged upstream; rebase recommended.**"
    echo
    echo "These commits are patch-id equivalent to commits already on '$base' (replayed work under different SHAs), so the PR diff is a pseudo-delta that buries the true change:"
    echo
    cat "$absorbed_md"
    echo
    echo "Base '$base' is also **$behind commits ahead** of this branch (threshold: $BEHIND_THRESHOLD). Rebase (git rebase $base) and force-push (git push --force-with-lease) so the true delta is reviewable; the pr_delta tool decomposes the split post-hoc."
  elif [ "$verdict" = "warn" ]; then
    echo "**Stale base: this branch is $behind commits behind '$base' (threshold: $BEHIND_THRESHOLD).**"
    echo
    echo "No replayed commits detected, but at this distance the PR diff starts mixing in upstream churn. Rebase recommended (git rebase $base) before review effort is spent."
  else
    echo "Base check clean: $behind behind '$base' (threshold: $BEHIND_THRESHOLD), $total branch commits, none replayed."
  fi
  echo
  echo "_Read-only guard fired at PR create/update; patch-id equivalence over merge-base..$base vs merge-base..head, same method as the pr_delta tool. Never blocks CI._"
} >"$body"

# ---- comment mode ------------------------------------------------------------
if [ -z "$GITHUB_TOKEN" ] || [ -z "$PR_NUMBER" ]; then
  log "detect-only mode (GITHUB_TOKEN/PR_NUMBER unset); verdict=$verdict"
  cat "$body"
  exit 0
fi
command -v gh >/dev/null 2>&1 || { log "gh not found; detect-only"; exit 0; }
GHC() { GH_TOKEN="$GITHUB_TOKEN" gh api "$@"; }

existing_id=""
comments="$(GHC "repos/$REPO/issues/$PR_NUMBER/comments?per_page=100" 2>/dev/null || echo '[]')"
existing_id="$(printf '%s' "$comments" | jq -r '.[] | select(.body != null and (.body | contains("stale-base-guard"))) | .id' | head -n 1)"
existing_body=""
if [ -n "$existing_id" ]; then
  existing_body="$(printf '%s' "$comments" | jq -j --arg id "$existing_id" '.[] | select(.id == ($id | tonumber)) | .body')"
fi

new_body="$(cat "$body")"
if [ "$existing_body" = "$new_body" ]; then
  log "guard comment already up to date (id=$existing_id); nothing to do"
  exit 0
fi

payload="$tmp/payload.json"
jq -n --rawfile b "$body" '{body: $b}' >"$payload"
if [ -z "$existing_id" ]; then
  if [ "$verdict" = "clean" ]; then
    log "clean and no prior guard comment; staying silent"
    exit 0
  fi
  if GHC -X POST --input "$payload" "repos/$REPO/issues/$PR_NUMBER/comments" >/dev/null 2>&1; then
    log "posted warning comment (verdict=$verdict)"
  else
    log "failed to post comment (token scope?); detect-only result stands"
  fi
else
  if GHC -X PATCH --input "$payload" "repos/$REPO/issues/comments/$existing_id" >/dev/null 2>&1; then
    log "updated guard comment $existing_id (verdict=$verdict)"
  else
    log "failed to update comment $existing_id; detect-only result stands"
  fi
fi
exit 0
