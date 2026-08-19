#!/usr/bin/env bash
# Deterministic review-coverage ledger for the cron review loops (cron-1/cron-2).
#
# Problem it fixes: "reviewed" state previously lived only in issue titles and
# agent memory, so files reviewed-and-clean left no trace - sampling was random
# and untraceable, and the long tail (most of ~1100 non-test .go files) was
# never deterministically swept.
#
# The ledger records which files received a FULL review, keyed by git blob SHA
# (content-based invalidation: edit the file -> stale, needs re-review).
# Format: exact `git ls-files -s` lines ("100644 <sha> 0\t<path>").
#
# Commands:
#   status                 coverage summary (total / reviewed / stale / unreviewed)
#   next <egrep> [n]       up to n paths needing review (unreviewed first, then
#                          stale, each alphabetical). egrep filters paths;
#                          leading '!' inverts the filter (complement scope).
#   mark <path>...         record the current blob SHA for each path - call this
#                          ONLY after a full-file review, not after a diff skim.
#   prune                  drop ledger entries for files deleted from the repo
#
# Scopes (deterministic long-tail sweep; diff-window review stays full-scope):
#   cron-2: next '^(internal/agent/|desktop/)' 5
#   cron-1: next '!^(internal/agent/|desktop/)' 5   (the complement)
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
LEDGER=.ggcode/review-ledger.txt

all_files() { git ls-files -s -- '*.go' | grep -v '_test\.go$' || true; }

case "${1:-status}" in
  status)
    td=$(mktemp -d)
    all_files | sort > "$td/cur"
    if [ -f "$LEDGER" ]; then sort "$LEDGER" > "$td/led"; else : > "$td/led"; fi
    total=$(wc -l < "$td/cur" | tr -d ' ')
    reviewed=$(comm -12 "$td/led" "$td/cur" | wc -l | tr -d ' ')
    gone=$(comm -23 "$td/led" "$td/cur" | wc -l | tr -d ' ')
    unreviewed=$(comm -13 "$td/led" "$td/cur" | wc -l | tr -d ' ')
    pct=0
    [ "$total" -gt 0 ] && pct=$(( reviewed * 100 / total ))
    echo "total=$total reviewed=$reviewed ($pct%) stale-or-deleted=$gone unreviewed=$unreviewed"
    rm -rf "$td"
    ;;
  next)
    pattern=${2:-.}
    n=${3:-5}
    td=$(mktemp -d)
    all_files | sort > "$td/cur"
    cut -f2 "$td/cur" | sort > "$td/curpaths"
    if [ -f "$LEDGER" ]; then sort "$LEDGER" > "$td/led"; else : > "$td/led"; fi
    cut -f2 "$td/led" | sort > "$td/ledpaths"
    # Unreviewed: tracked now, never marked.
    comm -13 "$td/ledpaths" "$td/curpaths" > "$td/unrev"
    # Stale: marked before, but current blob SHA differs.
    : > "$td/stale"
    while IFS= read -r p; do
      [ -n "$p" ] || continue
      cur_line=$(grep -F -m1 "$(printf '\t')$p" "$td/cur" || true)
      led_line=$(grep -F -m1 "$(printf '\t')$p" "$td/led" || true)
      if [ -n "$cur_line" ] && [ "$cur_line" != "$led_line" ]; then
        printf '%s\n' "$p" >> "$td/stale"
      fi
    done < <(comm -12 "$td/ledpaths" "$td/curpaths")
    cat "$td/unrev" "$td/stale" | sort -u > "$td/need"
    inv=0
    if [ "${pattern:0:1}" = "!" ]; then inv=1; pattern=${pattern:1}; fi
    if [ "$inv" = 1 ]; then
      { grep -vE "$pattern" "$td/need" || true; } | head -n "$n"
    else
      { grep -E "$pattern" "$td/need" || true; } | head -n "$n"
    fi
    rm -rf "$td"
    ;;
  mark)
    shift
    [ $# -ge 1 ] || { echo "usage: mark <path>..." >&2; exit 2; }
    touch "$LEDGER"
    for p in "$@"; do
      line=$(git ls-files -s -- "$p" || true)
      if [ -z "$line" ]; then echo "skip (not tracked): $p" >&2; continue; fi
      { grep -vF "$(printf '\t')$p" "$LEDGER" || true; } > "$LEDGER.tmp"
      printf '%s\n' "$line" >> "$LEDGER.tmp"
      mv "$LEDGER.tmp" "$LEDGER"
    done
    ;;
  prune)
    td=$(mktemp -d)
    all_files | sort > "$td/cur"
    [ -f "$LEDGER" ] || { rm -rf "$td"; exit 0; }
    sort "$LEDGER" | comm -12 - "$td/cur" > "$LEDGER.tmp"
    mv "$LEDGER.tmp" "$LEDGER"
    rm -rf "$td"
    ;;
  *)
    echo "usage: $0 {status|next <egrep> [n]|mark <path>...|prune}" >&2
    exit 2
    ;;
esac
