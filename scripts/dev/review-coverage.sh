#!/usr/bin/env bash

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
EPOCH_FILE=.ggcode/review-epoch.txt
EPOCH=$(cat "$EPOCH_FILE" 2>/dev/null || echo 1)
LEDGER=.ggcode/review-ledger.e${EPOCH}.txt

# Cross-platform ledger lock (mkdir is atomic everywhere; flock is Linux-only).
# Spin with timeout; stale locks older than 1 minute are broken forcibly.
ledger_lock() {
  local dir="${LEDGER}.lockd" i=0
  until mkdir "$dir" 2>/dev/null; do
    if [ -n "$(find "$dir" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
      rmdir "$dir" 2>/dev/null || rm -rf "$dir"   # stale lock - break it
      continue
    fi
    i=$((i+1))
    [ "$i" -gt 100 ] && { echo "ledger lock timeout ($dir)" >&2; exit 1; }
    sleep 0.1
  done
}
ledger_unlock() { rmdir "${LEDGER}.lockd" 2>/dev/null || true; }

all_files() {
  { git ls-files -s -- '*.go' | grep -v '_test\.go$' || true;
    git ls-files -s -- 'mobile/flutter/**/*.dart' | grep -vE '(\.g\.dart$|_test\.dart$)' || true; }
}

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
    echo "epoch=$EPOCH total=$total reviewed=$reviewed ($pct%) stale-or-deleted=$gone unreviewed=$unreviewed"
    if [ "$unreviewed" -eq 0 ] && [ "$gone" -eq 0 ]; then
      echo "CYCLE COMPLETE: epoch $EPOCH at 100% - run 'reset' to start the next full-review cycle from zero."
    fi
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

    comm -13 "$td/ledpaths" "$td/curpaths" > "$td/unrev"

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
    [ $# -lt 1 ] && { echo "usage: $0 mark <path>..." >&2; exit 1; }
    # Concurrency-safe: serialize the read-modify-write and use a unique tmp
    # file so parallel marks cannot lose entries or collide on a shared temp
    # path (issue #5390).
    ledger_lock; trap ledger_unlock EXIT
    touch "$LEDGER"
    marked=''
    for p in "$@"; do
      # #5483: `git ls-files ... || true` swallowed transient fork/exec
      # failures under CI memory pressure - the mark silently became a
      # "skip (not tracked)" and exited 0, losing the entry (observed as
      # "mark of pkg/fh.go lost" with every cmd green). Retry transient
      # failures; only an exit-0-but-empty result is a genuine skip.
      line=$(git ls-files -s -- "$p" 2>/dev/null) && git_ok=1 || git_ok=0
      for _try in 1 2 3; do
        [ "$git_ok" = 1 ] && break
        sleep 0.2
        line=$(git ls-files -s -- "$p" 2>/dev/null) && git_ok=1 || git_ok=0
      done
      if [ "$git_ok" != 1 ]; then
        echo "mark: git ls-files failed for $p (transient, gave up after retries)" >&2
        exit 1
      fi
      if [ -z "$line" ]; then echo "skip (not tracked): $p" >&2; continue; fi
      tmp=$(mktemp "${LEDGER}.XXXXXX")
      { grep -vF "$(printf '\t')$p" "$LEDGER" || true; } > "$tmp"
      printf '%s\n' "$line" >> "$tmp"
      mv "$tmp" "$LEDGER"
      marked="$marked $p"
    done
    # #5483 belt: every path actually written must be present in the ledger
    # on exit - any silent write loss becomes a loud failure instead of a
    # lost mark. Skipped (untracked) paths are intentionally exempt.
    for p in $marked; do
      if ! grep -qF "$(printf '\t')$p" "$LEDGER"; then
        echo "mark: $p missing from ledger after write" >&2
        exit 1
      fi
    done
    ;;
  prune)
    td=$(mktemp -d)
    all_files | sort > "$td/cur"
    [ -f "$LEDGER" ] || { rm -rf "$td"; exit 0; }
    # Concurrency-safe vs concurrent mark: same lock discipline (issue #5390).
    ledger_lock; trap ledger_unlock EXIT
    tmp=$(mktemp "${LEDGER}.XXXXXX")
    sort "$LEDGER" | comm -12 - "$td/cur" > "$tmp"
    mv "$tmp" "$LEDGER"
    rm -rf "$td"
    ;;
  reset)

    next=$(( EPOCH + 1 ))
    if [ -f "$LEDGER" ]; then :; else touch "$LEDGER"; fi
    echo "$next" > "$EPOCH_FILE"
    echo "epoch $EPOCH archived at $LEDGER; starting epoch $next from zero ($(all_files | wc -l | tr -d ' ') files)."
    ;;
  *)
    echo "usage: $0 {status|next <egrep> [n]|mark <path>...|prune|reset}" >&2
    exit 2
    ;;
esac
