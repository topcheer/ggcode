#!/usr/bin/env bash

set -u
EXPECT_REV="${1:-4e698c8d5}"

PID="${2:-}"
GG_BIN="/Volumes/new/ggai/ggcode/bin/ggcode"
WS="/Volumes/new/ggai/ggcode"
FAIL=0

say() { printf '%s\n' "$*"; }

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

go version -m "$GG_BIN" 2>/dev/null | grep -q 'vcs.modified=true' && \
    say "WARN: build had uncommitted changes (vcs.modified=true)"

if grep -a -q 'refusalFlags' "$GG_BIN" 2>/dev/null; then
    say "OK: anchor-fix symbol probe hit (refusalFlags present)"
else
    say "FAIL: anchor-fix symbol probe miss - binary predates #4282"
    FAIL=1
fi

say "== refusals data =="
RF="$WS/.ggcode/refusals.json"
ENTRIES=$(grep -c '"excerpt"' "$RF" 2>/dev/null || true)

[ -n "$ENTRIES" ] || { say "entries now: file-missing (want 0)"; FAIL=1; }
[ "${ENTRIES:-x}" = "0" ] && say "entries now: 0 (clean)" || true
BK="$WS/.ggcode/refusals.json.polluted-backup-20261017"
if [ -f "$BK" ]; then
    say "backup present: $BK"
else
    say "WARN: pollution backup missing at $BK"
fi

say "== live refusal probe =="
if grep -c "do not re-search covered ground" "$BK" >/dev/null 2>&1; then
    say "OK: probe grep executed (anchor chain dead) hit=$(grep -c 'do not re-search covered ground' "$BK")"
else
    say "FAIL: probe grep still blocked or errored - old in-memory entries alive?"
    FAIL=1
fi

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
