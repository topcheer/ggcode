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

# ── Config ────────────────────────────────────────────────────────────────
# Default GOMEMLIMIT matches GitHub CI (2GiB). Override via VERIFY_CI_MEMLIMIT.
GOMEMLIMIT="${VERIFY_CI_MEMLIMIT:-2GiB}"
# Cap build/test parallelism: `go build` and `go mod download` default -p to
# the machine core count; on shared/constrained runners the parallel
# compilation of large packages spikes peak RSS past the cgroup/OS limit and
# the toolchain gets OOM-killed ("signal: killed") before any code issue is
# reported. GOMAXPROCS caps every downstream go command in one knob. Override
# via VERIFY_CI_GOMAXPROCS on roomier machines.
export GOMAXPROCS="${VERIFY_CI_GOMAXPROCS:-2}"
# Set VERIFY_CI_FULL=1 to also run cross-compile, desktop, and frontend checks.
FULL="${VERIFY_CI_FULL:-0}"

# ── Main module (mirrors .github/workflows/ci.yml) ────────────────────────
echo "[verify-ci] checking gofmt cleanliness"
if ! test -z "$(gofmt -l ./cmd ./internal)"; then
  echo "[verify-ci] gofmt found unformatted files:"
  gofmt -l ./cmd ./internal
  exit 1
fi

echo "[verify-ci] downloading modules"
go mod download

echo "[verify-ci] building ggcode"
GOMEMLIMIT="${GOMEMLIMIT}" go build -tags goolm -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] running go vet"
# -p 1: vet defaults to -p=GOMAXPROCS; on shared/constrained runners the
# parallel type-checking of large packages (internal/agent, desktop) spikes
# peak RSS past GOMEMLIMIT and the process gets OOM-killed ("signal: killed")
# before any code issue is reported. Same rationale as the -p 1 on go test
# below. -p 2 still reproduced the OOM kill on shared machines, so 1 is the
# default; override via VERIFY_CI_VET_P.
GOMEMLIMIT="${GOMEMLIMIT}" go vet -tags goolm -p "${VERIFY_CI_VET_P:-1}" ./...

echo "[verify-ci] running tests (main module, unit only)"
# NOTE: do NOT use the "integration" tag here - integration tests (e.g. browser
# tests that spawn Chrome) are too heavy for CI and will OOM.
GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go test -tags goolm -p 1 -timeout 300s ./cmd/... ./internal/...

echo "[verify-ci] core checks passed"

# ── Optional full checks (VERIFY_CI_FULL=1) ───────────────────────────────
if [ "${FULL}" = "1" ]; then
  echo ""
  echo "[verify-ci:full] cross-platform compile check (linux + windows)"
  for target in "linux/amd64" "windows/amd64"; do
    os="${target%%/*}"
    arch="${target##*/}"
    if ! CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOMEMLIMIT="${GOMEMLIMIT}" go build -tags goolm ./cmd/ggcode 2>/tmp/cross-build.err; then
      echo "[verify-ci:full] cross-compile FAILED for ${os}/${arch}:"
      cat /tmp/cross-build.err
      exit 1
    fi
  done
  echo "[verify-ci:full] cross-platform compile check passed"

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
    (cd "${desktop_dir}" && CGO_ENABLED=1 GOMEMLIMIT="${GOMEMLIMIT}" go test -tags goolm -count=1 -timeout 120s ./...)
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

  echo ""
  echo "[verify-ci:full] all checks passed"
fi
