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

echo "[verify-ci] checking gofmt cleanliness"
if ! test -z "$(gofmt -l .)"; then
  echo "[verify-ci] gofmt found unformatted files:"
  gofmt -l .
  exit 1
fi

echo "[verify-ci] downloading modules"
go mod download

echo "[verify-ci] building ggcode"
go build -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] running go vet"
go vet ./...

echo "[verify-ci] running tests"
go test -tags=!integration ./...
