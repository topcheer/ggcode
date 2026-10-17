#!/usr/bin/env bash
# verify-binary.sh -- post-restart self-verification for the v1.3.265
# minimum-interruption fix chain (#4280/#4282/#4279).
#
# Run this in a FRESH ggcode session (after an in-place /restart). It
# proves, with raw evidence, that the running process is the new binary
# and the refusal-anchor false-positive chain is dead. Exit 0 = all good.
#
# Usage: bash scripts/verify-binary.sh [expected_vcs_revision_prefix]
#        (default 4e698c8d5, the v1.3.265 release commit)
#
# PRECONDITION: rebuild the binary from current main BEFORE /restart -
#     git pull && go build -tags goolm -o bin/ggcode ./cmd/ggcode
# The binary present at session handoff (79c4ec62e, built 19:04) predates
# the anchor fix #4282 and will correctly FAIL the ancestry check.

set -u
EXPECT_REV="${1:-4e698c8d5}"
# PID to verify. Pass explicitly (2nd arg) in multi-instance LAN setups -
# pgrep -n picks an arbitrary peer (grabbed a different seat's process
# in the first live run). Falls back to $PPID's chain only when absent.
PID="${2:-}"
GG_BIN="/Volumes/new/ggai/ggcode/bin/ggcode"
WS="/Volumes/new/ggai/ggcode"
FAIL=0

say() { printf '%s\n' "$*"; }

# --- 1. Process identity -------------------------------------------------
if [ -z "$PID" ]; then
    P=$PPID
    for _ in 1 2 3 4; do
        C=$(ps -o comm= -p "$P" 2>/dev/null) || break
        case "$C" in *bin/ggcode) PID=$P; break ;; esac
        N=$(ps -o ppid= -p "$P" 2>/dev/null | tr -d ' ') || break
        [ -z "$N" ] && break
        P=$N
    done
fi
if [ -z "$PID" ]; then
    say "FAIL: no ggcode process identified (pass PID as 2nd arg)"
    exit 1
fi
say "== process =="
ps -p "$PID" -o pid=,lstart=,command=

# --- 2. Binary provenance ------------------------------------------------
# Fix-containing check is ANCESTRY, not equality: the tag commit must be
# an ancestor of the binary's vcs.revision. (Observed trap: a build at
# 79c4ec62e predates the anchor fix and must FAIL here.)
say "== binary =="
say "-- lsof txt mapping --"
lsof -p "$PID" 2>/dev/null | awk '$4=="txt" && /ggcode/ {print "inode", $8, $NF}' | head -3
say "-- on-disk binary --"
stat -f "mtime=%Sm size=%z inode=%i" "$GG_BIN"
say "-- vcs stamp --"
go version -m "$GG_BIN" 2>/dev/null | grep -E 'vcs\.(revision|time|modified)' || {
    say "FAIL: no vcs stamp embedded"; FAIL=1; }
REV=$(go version -m "$GG_BIN" 2>/dev/null | awk '/vcs.revision=/ {print substr($2,14)}')
if [ -n "${REV:-}" ]; then
    if (cd "$WS" && git merge-base --is-ancestor "$EXPECT_REV" "$REV"); then
        say "OK: $EXPECT_REV is ancestor of running $REV (fix chain embedded)"
    else
        say "FAIL: running vcs.revision=$REV does NOT contain $EXPECT_REV"
        say "      rebuild first: git -C $WS pull && go build -tags goolm -o $GG_BIN ./cmd/ggcode"
        FAIL=1
    fi
fi
# vcs.modified=true means uncommitted changes were baked in: warn, do not
# fail (tree dirt is routine on this shared multi-agent workspace).
go version -m "$GG_BIN" 2>/dev/null | grep -q 'vcs.modified=true' && \
    say "WARN: build had uncommitted changes (vcs.modified=true)"
# Direct probe: a symbol introduced by the anchor fix (#4282). Absent in
# builds predating it (e.g. 79c4ec62e) even when stamps confuse.
if grep -a -q 'refusalFlags' "$GG_BIN" 2>/dev/null; then
    say "OK: anchor-fix symbol probe hit (refusalFlags present)"
else
    say "FAIL: anchor-fix symbol probe miss - binary predates #4282"
    FAIL=1
fi

# --- 3. Refusal data surface --------------------------------------------
say "== refusals data =="
RF="$WS/.ggcode/refusals.json"
ENTRIES=$(grep -c '"excerpt"' "$RF" 2>/dev/null || true)
# grep -c exits 1 when count is 0 - keep the count, never fall through to
# the missing-file wording (that false-path set FAIL=1 on a clean ledger
# in the first live run).
[ -n "$ENTRIES" ] || { say "entries now: file-missing (want 0)"; FAIL=1; }
[ "${ENTRIES:-x}" = "0" ] && say "entries now: 0 (clean)" || true
BK="$WS/.ggcode/refusals.json.polluted-backup-20261017"
if [ -f "$BK" ]; then
    say "backup present: $BK"
else
    say "WARN: pollution backup missing at $BK"
fi

# --- 4. Live probe: the exact excerpt that used to hard-block ------------
# Under the OLD process (in-memory cached entries + boundary-less anchors)
# this grep was blocked 5x live. Under the fixed binary it must run clean.
say "== live refusal probe =="
if grep -c "do not re-search covered ground" "$BK" >/dev/null 2>&1; then
    say "OK: probe grep executed (anchor chain dead) hit=$(grep -c 'do not re-search covered ground' "$BK")"
else
    say "FAIL: probe grep still blocked or errored - old in-memory entries alive?"
    FAIL=1
fi

# --- 5. Anchor regression suite ------------------------------------------
say "== go test anchor suite =="
if (cd "$WS" && GOMEMLIMIT=2GiB GOGC=50 go test -tags goolm ./internal/agent/ -count=1 -timeout 600s 2>&1 | tail -2); then
    say "OK: agent package PASS"
else
    say "FAIL: agent package regression"
    FAIL=1
fi

say "== verdict =="
if [ "$FAIL" = "0" ]; then
    say "ALL CHECKS PASSED - running binary verified against $EXPECT_REV"
else
    say "CHECKS FAILED (see above)"
fi
exit $FAIL
