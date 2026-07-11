#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

# Clear inherited Git execution context so nested temp repos in tests behave like CI.
while IFS='=' read -r name _; do
  case "${name}" in
    GIT_*)
      unset "${name}"
      ;;
  esac
done < <(env)

export CGO_ENABLED="${CGO_ENABLED:-0}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
export GIT_CONFIG_GLOBAL="${GIT_CONFIG_GLOBAL:-/dev/null}"
export GIT_CONFIG_SYSTEM="${GIT_CONFIG_SYSTEM:-/dev/null}"
unset ZAI_API_KEY
unset GGCODE_ZAI_API_KEY
unset ZAI_MODEL

# ── Main module ──────────────────────────────────────────────────────────
echo "[verify-ci] checking gofmt cleanliness (main module)"
if ! test -z "$(gofmt -l ./cmd ./internal)"; then
  echo "[verify-ci] gofmt found unformatted files:"
  gofmt -l ./cmd ./internal
  exit 1
fi

echo "[verify-ci] downloading modules"
go mod download

echo "[verify-ci] building ggcode"
# Limit parallelism and heap to prevent OOM kills on memory-constrained CI runners.
GOMEMLIMIT=2GiB go build -tags goolm -p 2 -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] cross-platform compile check (linux + windows)"
# Catch errors in platform-specific files (*_darwin.go, *_linux.go, *_windows.go)
# that only surface when building for a different OS than the dev machine.
for target in "linux/amd64" "windows/amd64"; do
  os="${target%%/*}"
  arch="${target##*/}"
  if ! CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOMEMLIMIT=1GiB go build -tags goolm -p 2 ./cmd/ggcode 2>/tmp/cross-build.err; then
    echo "[verify-ci] cross-compile FAILED for ${os}/${arch}:"
    cat /tmp/cross-build.err
    exit 1
  fi
done
echo "[verify-ci] cross-platform compile check passed"

echo "[verify-ci] running go vet (main module)"
GOMEMLIMIT=2GiB go vet -tags goolm -p 2 ./cmd/... ./internal/...

echo "[verify-ci] running tests (main module, unit only)"
# NOTE: do NOT use the "integration" tag here — integration tests (e.g. browser
# tests that spawn Chrome) are too heavy for CI and will OOM. Run them
# separately via: go test -tags "goolm,integration" ./internal/tool/ -run TestBrowserIntegration
# 8GiB limit gives comfortable headroom for large test packages (tui, agent, a2a).
# Run tests in two batches to reduce peak memory usage.
# Split: lightweight packages first (cmd, small internal pkgs), then heavy ones.
GOMEMLIMIT=8GiB go test -tags goolm -p 1 -parallel 1 -timeout 300s ./cmd/... ./internal/agent/... ./internal/config/... ./internal/context/... ./internal/provider/... ./internal/session/... ./internal/util/...
GOMEMLIMIT=8GiB go test -tags goolm -p 1 -parallel 1 -timeout 300s ./internal/a2a/... ./internal/acp/... ./internal/cron/... ./internal/debug/... ./internal/tui/extpane/... ./internal/im/... ./internal/mcp/... ./internal/permission/... ./internal/plugin/... ./internal/runfile/... ./internal/stream/... ./internal/tool/... ./internal/tui/... ./internal/tunnel/... ./internal/update/... ./internal/vcs/... ./internal/webui/...

# ── Desktop module (CGO required, macOS only) ────────────────────────────
desktop_dir="${repo_root}/desktop/ggcode-desktop-wails"
if [ -d "${desktop_dir}" ] && [ -f "${desktop_dir}/go.mod" ]; then
  echo ""
  echo "[verify-ci:desktop] checking gofmt cleanliness"
  if ! test -z "$(gofmt -l "${desktop_dir}")"; then
    echo "[verify-ci:desktop] gofmt found unformatted files:"
    gofmt -l "${desktop_dir}"
    exit 1
  fi

  echo "[verify-ci:desktop] downloading modules"
  (cd "${desktop_dir}" && go mod download)

  echo "[verify-ci:desktop] running go vet"
  (cd "${desktop_dir}" && CGO_ENABLED=1 go vet -tags goolm ./...)

  echo "[verify-ci:desktop] running tests"
  (cd "${desktop_dir}" && CGO_ENABLED=1 GOMEMLIMIT=2GiB go test -tags goolm -p 1 -parallel 1 -count=1 -timeout 120s ./...)
fi

# ── Frontend Vitest (no CGO needed) ───────────────────────────────────────
frontend_dir="${desktop_dir}/frontend"
if [ -d "${frontend_dir}" ] && [ -f "${frontend_dir}/package.json" ]; then
  echo ""
  echo "[verify-ci:frontend] running Vitest tests"
  (cd "${frontend_dir}" && npx vitest run --reporter=dot 2>&1)
  if [ $? -ne 0 ]; then
    echo "[verify-ci:frontend] Vitest tests FAILED"
    exit 1
  fi
  echo "[verify-ci:frontend] Vitest tests passed"
fi
