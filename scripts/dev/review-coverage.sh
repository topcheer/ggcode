#!/usr/bin/env bash

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
EPOCH_FILE=.ggcode/review-epoch.txt
EPOCH=$(cat "$EPOCH_FILE" 2>/dev/null || echo 1)
LEDGER=.ggcode/review-ledger.e${EPOCH}.txt

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
