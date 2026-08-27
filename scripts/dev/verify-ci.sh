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
# Default 1 (not 2): the Go compiler is internally parallel per package even
# with -p 1; on shared machines with concurrent agent workloads GOMAXPROCS=2
# still reproduced the "signal: killed" OOM during compilation of the largest
# packages (internal/agent, cmd/ggcode). 1 trades wall-clock for reliability.
export GOMAXPROCS="${VERIFY_CI_GOMAXPROCS:-1}"
# Package-level parallelism: `go test ./...` defaults -p to core count;
# on memory-constrained dev machines (macOS under memory pressure) the
# parallel test binaries spike peak RSS and get OOM-killed with a bare
# "signal: killed" before any real failure is reported. Must be the
# -p=1 equals form: GOFLAGS="-p 1" is rejected by go (non-flag "1").
export GOFLAGS="${GOFLAGS:--p=1}"
# Set VERIFY_CI_FULL=1 to also run cross-compile, desktop, and frontend checks.
FULL="${VERIFY_CI_FULL:-0}"

# avail_mem_mb: best-effort available system memory in MiB (0 if unknown).
# macOS: vm_stat free+inactive+speculative pages. Linux: /proc/meminfo MemAvailable.
avail_mem_mb() {
  if command -v vm_stat >/dev/null 2>&1; then
    vm_stat | awk '
      /page size of/ { ps = $8 }
      /^Pages free/ { free = $3 }
      /^Pages inactive/ { inact = $3 }
      /^Pages speculative/ { spec = $3 }
      END {
        gsub(/\./, "", free); gsub(/\./, "", inact); gsub(/\./, "", spec)
        if (ps + 0 > 0) printf "%d", (free + inact + spec) * ps / 1048576
      }'
  elif [ -r /proc/meminfo ]; then
    awk '/^MemAvailable:/ { printf "%d", $2 / 1024 }' /proc/meminfo
  fi
}

# wait_for_memory: block until available memory recovers (>=1500 MiB) or
# max wait elapses. Returns as soon as pressure clears.
wait_for_memory() {
  local waited=0 avail
  while [ "${waited}" -lt 180 ]; do
    avail="$(avail_mem_mb)"
    if [ -z "${avail}" ] || [ "${avail}" -ge 1500 ]; then
      return 0
    fi
    sleep 10
    waited=$((waited + 10))
  done
  return 0
}

# run_with_oom_retry <desc> <cmd...>
# Runs cmd; if it is OOM-killed ("signal: killed" or exit 137), waits for the
# system memory pressure on shared machines to actually subside (polling
# available RAM, not a blind sleep), then retries — up to 3 attempts with
# escalating backoff. A bare OOM kill carries no code signal; real errors
# still fail immediately with the original exit code.
run_with_oom_retry() {
  local desc="$1"; shift
  local log status attempt backoff
  log="$(mktemp -t verifyci)"
  backoff=20
  for attempt in 1 2 3; do
    # Gate before EVERY attempt (incl. the first): on shared machines a
    # concurrent agent workload can spike memory between the startup gate and
    # this step; proceeding into the spike gets the first attempt killed.
    # Waiting here (up to 3min) converts the spike into a delayed first run
    # instead of a burned attempt. Cheap no-op when memory is plentiful.
    if [ "${VERIFY_CI_SKIP_MEM_GATE:-0}" != "1" ]; then
      _avail="$(avail_mem_mb)"
      if [ -n "${_avail}" ] && [ "${_avail}" -lt 1500 ]; then
        echo "[verify-ci] ${desc}: low available memory (${_avail}MiB) before attempt ${attempt}; waiting up to 3min for pressure to subside"
        wait_for_memory
      fi
    fi
    if "$@" >"${log}" 2>&1; then
      cat "${log}"
      rm -f "${log}"
      return 0
    fi
    status=$?
    if grep -q "signal: killed" "${log}" || [ "${status}" -eq 137 ]; then
      if [ "${attempt}" -eq 3 ]; then
        echo "[verify-ci] ${desc} was OOM-killed 3 times; giving up"
        cat "${log}"
        rm -f "${log}"
        return 1
      fi
      echo "[verify-ci] ${desc} was OOM-killed (attempt ${attempt}/3); waiting up to 3min for memory pressure to subside (avail=$(avail_mem_mb 2>/dev/null || echo '?')MiB)"
      wait_for_memory
      sleep "${backoff}"
      backoff=$((backoff * 3))
      continue
    fi
    cat "${log}"
    rm -f "${log}"
    return "${status}"
  done
  return 1
}

# Startup memory gate: on shared machines with concurrent agent workloads the
# run may start while memory pressure is already high; waiting here (up to
# 3min) lets that pressure clear BEFORE the first heavy go command runs, so a
# kill lands inside run_with_oom_retry (retryable) instead of on an unwrapped
# step (fatal). Override with VERIFY_CI_SKIP_MEM_GATE=1.
if [ "${VERIFY_CI_SKIP_MEM_GATE:-0}" != "1" ]; then
  _avail="$(avail_mem_mb)"
  if [ -n "${_avail}" ] && [ "${_avail}" -lt 1500 ]; then
    echo "[verify-ci] low available memory (${_avail}MiB); waiting up to 3min for pressure to subside"
    wait_for_memory
  fi
fi

# ── Main module (mirrors .github/workflows/ci.yml) ────────────────────────
echo "[verify-ci] checking gofmt cleanliness"
if ! test -z "$(gofmt -l ./cmd ./internal)"; then
  echo "[verify-ci] gofmt found unformatted files:"
  gofmt -l ./cmd ./internal
  exit 1
fi

echo "[verify-ci] downloading modules"
# Wrapped: mod download/extract spikes RSS on large go.sum sets and is killed
# with a bare "signal: killed" under memory pressure — same class as the
# build/vet/test steps below.
run_with_oom_retry "go mod download" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go mod download

echo "[verify-ci] building ggcode"
# -p 1 on build too: parallel compilation of large packages (desktop/wailskit,
# internal/agent) spikes peak RSS past GOMEMLIMIT on shared machines and gets
# OOM-killed ("signal: killed") - same rationale as the -p 1 on vet/test below.
run_with_oom_retry "go build" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go build -p 1 -tags goolm -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] running go vet"
# -p 1: vet defaults to -p=GOMAXPROCS; on shared/constrained runners the
# parallel type-checking of large packages (internal/agent, desktop) spikes
# peak RSS past GOMEMLIMIT and the process gets OOM-killed ("signal: killed")
# before any code issue is reported. Same rationale as the -p 1 on go test
# below. -p 2 still reproduced the OOM kill on shared machines, so 1 is the
# default; override via VERIFY_CI_VET_P.
run_with_oom_retry "go vet" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go vet -tags goolm -p "${VERIFY_CI_VET_P:-1}" ./...

echo "[verify-ci] running tests (main module, unit only)"
# NOTE: do NOT use the "integration" tag here - integration tests (e.g. browser
# tests that spawn Chrome) are too heavy for CI and will OOM.
# -parallel 1: same rationale as the Makefile `test` target - packages using
# t.Parallel() still spike peak RSS when many test funcs run concurrently on
# memory-constrained machines; run one test func at a time.
run_with_oom_retry "go test" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go test -tags goolm -p 1 -parallel 1 -timeout 300s ./cmd/... ./internal/...

echo "[verify-ci] core checks passed"

# ── Optional full checks (VERIFY_CI_FULL=1) ───────────────────────────────
if [ "${FULL}" = "1" ]; then
  echo ""
  echo "[verify-ci:full] cross-platform compile check (linux + windows)"
  for target in "linux/amd64" "windows/amd64"; do
    os="${target%%/*}"
    arch="${target##*/}"
    # Wrapped (same OOM-retry class as core steps): cross-compilation of the
    # full dependency graph spikes peak RSS identically to the native build.
    if ! run_with_oom_retry "cross-compile ${os}/${arch}" env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go build -tags goolm ./cmd/ggcode; then
      echo "[verify-ci:full] cross-compile FAILED for ${os}/${arch}"
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
    (cd "${desktop_dir}" && run_with_oom_retry "desktop go mod download" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go mod download)

    echo "[verify-ci:desktop] running go vet"
    (cd "${desktop_dir}" && run_with_oom_retry "desktop go vet" env CGO_ENABLED=1 GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go vet -tags goolm ./...)

    echo "[verify-ci:desktop] running tests"
    (cd "${desktop_dir}" && run_with_oom_retry "desktop go test" env CGO_ENABLED=1 GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go test -tags goolm -count=1 -timeout 120s ./...)
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
