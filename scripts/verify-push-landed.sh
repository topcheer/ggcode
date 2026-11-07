#!/usr/bin/env bash
# verify-push-landed.sh - post-push reconciliation assertion (#5262).
#
# Live near-miss during v1.3.277: with remote main one release commit
# behind local, `git push origin main` still printed "Everything
# up-to-date" (suspected pre-push hook refreshing the remote ref, or a
# stale origin/main cache). Only ls-remote discipline caught it - a
# pushed-but-not-landed release would leave an ORPHAN TAG that CI and
# users can never see (release-process.md: "遗漏 git push 是最常见的
# 发版错误").
#
# This helper makes that discipline non-optional: run it right after
# every push (main and/or tag). It asserts local HEAD, the remote ref,
# and (when given) the remote tag all point at the SAME SHA. Anything
# else exits non-zero with the mismatch spelled out.
#
# Usage:
#   scripts/verify-push-landed.sh main [tag-name]
#   scripts/verify-push-landed.sh main v1.3.277
#
# Exit 0 = local HEAD == remote main == remote tag (when given).
# Exit 1 = mismatch (each side's SHA printed).
# Exit 2 = usage error.
set -euo pipefail

REMOTE="${GGCODE_RELEASE_REMOTE:-origin}"

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 <branch> [tag]" >&2
  echo "example: $0 main v1.3.277" >&2
  exit 2
fi

branch="$1"
tag="${2:-}"

local_head="$(git rev-parse HEAD)"

if ! remote_refs="$(git ls-remote "$REMOTE" "refs/heads/$branch" ${tag:+"refs/tags/$tag"} 2>/dev/null)"; then
  echo "VERIFY-PUSH FAILED: cannot reach $REMOTE (network?)" >&2
  exit 1
fi

remote_branch="$(printf '%s\n' "$remote_refs" | awk -v ref="refs/heads/$branch" '$2 == ref {print $1}')"
if [[ -z "$remote_branch" ]]; then
  echo "VERIFY-PUSH FAILED: $REMOTE/$branch does not exist on the remote" >&2
  exit 1
fi

status=0
if [[ "$local_head" != "$remote_branch" ]]; then
  echo "VERIFY-PUSH FAILED: local HEAD != $REMOTE/$branch (push did NOT land - 'Everything up-to-date' may be a false positive, see #5262)" >&2
  echo "  local  HEAD : $local_head" >&2
  echo "  remote main : $remote_branch" >&2
  status=1
fi

if [[ -n "$tag" ]]; then
  # Peeled ^{} line = the commit an annotated tag points at; prefer it.
  remote_tag="$(printf '%s\n' "$remote_refs" | awk -v ref="refs/tags/$tag^{}" '$2 == ref {print $1}')"
  [[ -z "$remote_tag" ]] && remote_tag="$(printf '%s\n' "$remote_refs" | awk -v ref="refs/tags/$tag" '$2 == ref {print $1}')"
  if [[ -z "$remote_tag" ]]; then
    echo "VERIFY-PUSH FAILED: tag $tag is NOT on the remote (orphan tag - release invisible to CI/users)" >&2
    echo "  local  HEAD : $local_head" >&2
    status=1
  elif [[ "$remote_tag" != "$local_head" ]]; then
    echo "VERIFY-PUSH FAILED: $REMOTE tag $tag points elsewhere (lightweight-vs-annotated or stale tag)" >&2
    echo "  local  HEAD  : $local_head" >&2
    echo "  remote $tag : $remote_tag" >&2
    status=1
  fi
fi

if (( status == 0 )); then
  echo "verify-push passed: local HEAD == $REMOTE/$branch${tag:+ == $REMOTE tag $tag} == ${local_head:0:9}"
fi
exit "$status"
