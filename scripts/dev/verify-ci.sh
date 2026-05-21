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
go build -tags goolm -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] running go vet (main module)"
go vet -tags goolm ./cmd/... ./internal/...

echo "[verify-ci] running tests (main module, unit + Tier 1 integration)"
go test -tags "goolm,integration" ./cmd/... ./internal/...

# ── Desktop module (CGO required, macOS only) ────────────────────────────
desktop_dir="${repo_root}/desktop/ggcode-desktop"
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
  (cd "${desktop_dir}" && CGO_ENABLED=1 go test -tags goolm -count=1 ./...)
fi
