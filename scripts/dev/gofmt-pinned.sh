#!/usr/bin/env bash
# gofmt pinned to the go.mod-declared toolchain (#5241).
#
# The machine's default gofmt can be newer than the toolchain go.mod pins
# (e.g. local 1.27.3 vs pinned 1.26.10). A newer gofmt then silently
# re-formats staged files back to its own style and flags main's
# CI-clean files as unformatted, so local gates permanently disagree
# with CI (which pins the toolchain). Every gate that formats or checks
# Go formatting must invoke gofmt through this wrapper so local verdicts
# match main's.
#
# Usage: gofmt-pinned.sh <gofmt args...>
set -euo pipefail

root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
pinned="$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' "${root}/go.mod" 2>/dev/null | head -1)"

# No parseable "go <version>" line in go.mod: nothing to pin against.
if [[ -z "${pinned}" ]]; then
  exec gofmt "$@"
fi

active="$(go env GOVERSION 2>/dev/null || true)"
if [[ "${active}" == "go${pinned}" ]]; then
  # The go command already runs the pinned toolchain, so the gofmt on PATH
  # is the one main is formatted with (CI hits this via GOTOOLCHAIN=local).
  exec gofmt "$@"
fi

# Switch the go command to the pinned toolchain and run its gofmt.
# The per-invocation GOTOOLCHAIN override also beats an outer
# GOTOOLCHAIN=local (e.g. inside verify-ci.sh on a developer machine).
want="go${pinned}"
got="$(GOTOOLCHAIN="${want}" go env GOVERSION 2>/dev/null || true)"
if [[ "${got}" == "${want}" ]]; then
  exec env GOTOOLCHAIN="${want}" go run cmd/gofmt "$@"
fi

# Pinned toolchain unavailable (offline first run, or a forbid-listed
# toolchain switch): keep the gate usable, but be loud about the
# fallback instead of silently using the wrong formatting version.
echo "[gofmt-pinned] WARNING: pinned toolchain ${want} unavailable, falling back to default gofmt (${active:-unknown}); formatting verdicts may not match CI" >&2
exec gofmt "$@"
