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
# Default GOMEMLIMIT scales to the environment's REAL memory budget (see
# derivation below). The historical fixed value was 2GiB to match GitHub CI,
# but a hard 2GiB per-worker cap OOM-kills runs inside cgroup-limited
# verification sandboxes whose total budget is at or below the cap - go
# workers climb straight into the ceiling and the kernel kills them with the
# bare "signal: killed" the retry machinery kept seeing. Explicit override
# via VERIFY_CI_MEMLIMIT still wins.
if [ -n "${VERIFY_CI_MEMLIMIT:-}" ]; then
  GOMEMLIMIT="${VERIFY_CI_MEMLIMIT}"
else
  # Placeholder; sized precisely once memory probes exist (below).
  GOMEMLIMIT="2GiB"
fi
# NOTE: build/vet/test concurrency is NOT pinned here anymore. A fixed
# -p 1 -parallel 1 GOMAXPROCS=1 mode is safe under memory pressure but ~10x
# slower; on a roomy machine a cold-cache run then exceeds the caller's own
# timeout and gets SIGKILLed with an identical bare "signal: killed" — the
# same symptom the OOM mitigations were built for, reproduced by a different
# cause (timeout kill, not OS OOM). Concurrency is picked adaptively from LIVE
# available memory (pick_tier below) and auto-downgraded on every OOM kill
# inside run_with_oom_retry. GOMEMLIMIT stays as the per-process soft cap, so
# even the highest tier bounds each compile/test worker.
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

# cgroup_free_mb: free headroom INSIDE this process's cgroup limit, in MiB
# (empty when unknown or unlimited). On containerized CI runners /proc/meminfo
# reports HOST memory, but the kernel OOM-kills against the CGROUP limit - so
# sizing the -p tier off host stats picks high concurrency deterministically,
# and parallel compile/test workers then push RSS past the quota and every
# attempt dies with a bare "signal: killed" before any retry can help.
cgroup_free_mb() {
  local lim cur free_bytes
  # cgroup v2 unified hierarchy
  if [ -r /sys/fs/cgroup/memory.max ] && [ -r /sys/fs/cgroup/memory.current ]; then
    lim="$(cat /sys/fs/cgroup/memory.max 2>/dev/null || true)"
    cur="$(cat /sys/fs/cgroup/memory.current 2>/dev/null || true)"
    case "${lim}" in
      max|-1|"") return ;;
    esac
    [ -z "${cur}" ] && return
    free_bytes=$((lim - cur))
    if [ "${free_bytes}" -gt 0 ]; then
      printf '%d' "$((free_bytes / 1048576))"
    else
      printf '0'
    fi
    return
  fi
  # cgroup v1 (legacy); limits at/above 256GiB are page-counter sentinels
  # meaning unlimited, not real quotas.
  if [ -r /sys/fs/cgroup/memory/memory.limit_in_bytes ] && \
     [ -r /sys/fs/cgroup/memory/memory.usage_in_bytes ]; then
    lim="$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes 2>/dev/null || true)"
    cur="$(cat /sys/fs/cgroup/memory/memory.usage_in_bytes 2>/dev/null || true)"
    case "${lim}" in ''|*[!0-9]*) return ;; esac
    case "${cur}" in ''|*[!0-9]*) return ;; esac
    if [ "${lim}" -le 274877906944 ]; then
      free_bytes=$((lim - cur))
      if [ "${free_bytes}" -gt 0 ]; then
        printf '%d' "$((free_bytes / 1048576))"
      else
        printf '0'
      fi
    fi
  fi
}

# effective_avail_mb: min(host available, cgroup free); falls back to the host
# number whenever one side is unknown. All tier-picking and mem-gate checks
# below must use THIS function - it is the only value the OOM killer respects.
effective_avail_mb() {
  local h c
  h="$(avail_mem_mb 2>/dev/null || true)"
  c="$(cgroup_free_mb 2>/dev/null || true)"
  if [ -n "${c}" ] && { [ -z "${h}" ] || [ "${c}" -lt "${h}" ]; }; then
    printf '%s' "${c}"
  else
    printf '%s' "${h}"
  fi
}

# wait_for_memory: block until available memory recovers (>=1500 MiB) or a
# 120s cap elapses. Philosophy: forward progress at tier 1 beats waiting.
# Monitored/caller-timed runs die by wall-clock just as surely as by OOM,
# so this is a short breather for a transient spike to pass - never a long
# stall. The 10min predecessor caused exactly that stall: under sustained
# co-tenant pressure the run spent its whole life sleeping in gates and was
# killed for slowness with the same bare "signal: killed".
wait_for_memory() {
  local waited=0 avail
  while [ "${waited}" -lt 120 ]; do
    avail="$(effective_avail_mb)"
    if [ -z "${avail}" ] || [ "${avail}" -ge 1500 ]; then
      return 0
    fi
    sleep 10
    waited=$((waited + 10))
  done
  return 0
}

# pick_tier: set build/vet/test concurrency from live available memory.
#   low  (<3GiB avail or unknown): -p 1  -parallel 1  GOMAXPROCS=1 (safe mode)
#   mid  (3-10GiB):                -p 2  -parallel 2  GOMAXPROCS=2
#   high (>=10GiB):                -p 4  -parallel 4  GOMAXPROCS=4
# The mem-gate threshold scales with the tier: starting -p 4 safely needs more
# headroom than starting -p 1.
# Load cap: free memory measured at selection time does not account for what
# concurrent agent workloads are ABOUT to allocate. On shared machines the
# same co-tenant builds that spike memory mid-run show up as elevated load
# average first, so the 1-minute load is used to cap the tier: >=6 caps at
# mid (-p 2), >=12 caps at safe (-p 1). This only ever lowers a tier, never
# raises one - an idle machine behaves exactly as before.
VC_P=1; VC_PARALLEL=1; VC_GOMAXPROCS=1; VC_MEM_GATE_MB=1500
vc_load_1m() {
  # Portable 1-minute load average: Linux /proc/loadavg, macOS sysctl, then
  # uptime as last resort. Prints a single float or nothing.
  if [ -r /proc/loadavg ]; then
    head -1 /proc/loadavg 2>/dev/null | awk '{print $1; exit}'
  elif sysctl -n vm.loadavg >/dev/null 2>&1; then
    sysctl -n vm.loadavg 2>/dev/null | awk '{print $2; exit}'
  elif command -v uptime >/dev/null 2>&1; then
    uptime 2>/dev/null | sed -n 's/.*load averages\{0,1\}: *\([0-9.]*\).*/\1/p'
  fi
}
pick_tier() {
  local avail; avail="$(effective_avail_mb 2>/dev/null || true)"
  # Tier-4 headroom raised from 8192MiB to 10240MiB: -p 4 workers each hold
  # up to GOMEMLIMIT (2GiB), and on shared machines co-tenant agent builds can
  # push the margin negative right after the check, producing OOM kills that
  # burn retry attempts. Requiring real headroom before entering tier 4 keeps
  # most runs in a tier they can finish without downgrades.
  if [ -n "${avail}" ] && [ "${avail}" -ge 10240 ]; then
    VC_P=4; VC_PARALLEL=4; VC_GOMAXPROCS=4; VC_MEM_GATE_MB=4096
  elif [ -n "${avail}" ] && [ "${avail}" -ge 3072 ]; then
    VC_P=2; VC_PARALLEL=2; VC_GOMAXPROCS=2; VC_MEM_GATE_MB=2048
  else
    VC_P=1; VC_PARALLEL=1; VC_GOMAXPROCS=1; VC_MEM_GATE_MB=1500
  fi
  local load; load="$(vc_load_1m)"
  if [ -n "${load}" ]; then
    local cap
    cap="$(awk -v l="${load}" 'BEGIN { if (l >= 12) print 1; else if (l >= 6) print 2; else print 4 }')"
    if [ "${VC_P}" -gt "${cap}" ]; then
      VC_P="${cap}"; VC_PARALLEL="${cap}"; VC_GOMAXPROCS="${cap}"
      [ "${VC_P}" -le 1 ] && VC_MEM_GATE_MB=1500 || VC_MEM_GATE_MB=2048
      echo "[verify-ci] load cap applied (load ${load} -> -p ${VC_P})"
    fi
  fi
  export GOMAXPROCS="${VC_GOMAXPROCS}"
  echo "[verify-ci] concurrency tier: -p ${VC_P} -parallel ${VC_PARALLEL} GOMAXPROCS=${VC_GOMAXPROCS} (avail ${avail:-?}MiB, load ${load:-?})"
}
# downgrade_tier: halve concurrency (floor 1) after an OOM kill; retrying at
# the same concurrency under the same pressure just burns attempts.
downgrade_tier() {
  if [ "${VC_P}" -gt 1 ]; then
    VC_P=$((VC_P / 2)); VC_PARALLEL=$((VC_PARALLEL / 2)); VC_GOMAXPROCS=$((VC_GOMAXPROCS / 2))
    export GOMAXPROCS="${VC_GOMAXPROCS}"
    echo "[verify-ci] concurrency downgraded to -p ${VC_P} -parallel ${VC_PARALLEL} GOMAXPROCS=${VC_GOMAXPROCS}"
  fi
}

# ── Single-instance lock ─────────────────────────────────────────────────
# On shared machines several agents (and the verification harness itself)
# may launch verify-ci at once. Two full suites at tier 4 stack their
# GOMEMLIMIT peaks (~4GiB+ combined) and OOM-kill each other with a bare
# "signal: killed" before any per-chunk retry can help. Serialize instead:
# a second invocation waits for the first to finish, then runs uncontended.
# Fail-open after 5min so a wedged or slow holder cannot cost the caller
# more wall-clock than a contended run would (the tier gates below make a
# fail-open double-run self-regulating: both suites downgrade under the
# mutual memory/load pressure).
VC_LOCK_DIR="${TMPDIR:-/tmp}/ggcode-verify-ci.lock"
VC_LOCK_WAITED=0
while ! mkdir "${VC_LOCK_DIR}" 2>/dev/null; do
  # Recover a stale lock whose owner process no longer exists.
  _lock_pid="$(cat "${VC_LOCK_DIR}/pid" 2>/dev/null || true)"
  if [ -n "${_lock_pid}" ] && ! kill -0 "${_lock_pid}" 2>/dev/null; then
    echo "[verify-ci] removing stale verify-ci lock from dead pid ${_lock_pid}"
    rm -rf "${VC_LOCK_DIR}"
    continue
  fi
  # A holder that never wrote a pid file (killed in the mkdir->echo window;
  # SIGKILL fires no traps) is unrecoverable by pid - fall back to lock age.
  # A healthy holder writes pid within milliseconds, so a lock dir older
  # than 30s with no pid file is definitively orphaned.
  if [ -z "${_lock_pid}" ]; then
    _lock_age=$(( $(date +%s) - $(stat -f %m "${VC_LOCK_DIR}" 2>/dev/null || stat -c %Y "${VC_LOCK_DIR}" 2>/dev/null || echo 0) ))
    if [ "${_lock_age}" -ge 30 ]; then
      echo "[verify-ci] removing orphan verify-ci lock (no pid file, age ${_lock_age}s)"
      rm -rf "${VC_LOCK_DIR}"
      continue
    fi
  fi
  if [ "${VC_LOCK_WAITED}" -ge 300 ]; then
    echo "[verify-ci] lock still held after 5min; proceeding without serialization"
    break
  fi
  if [ "${VC_LOCK_WAITED}" -eq 0 ]; then
    echo "[verify-ci] another verify-ci run is active; waiting for it to finish (up to 5min)"
  fi
  sleep 10
  VC_LOCK_WAITED=$((VC_LOCK_WAITED + 10))
done
trap 'rm -rf "${VC_LOCK_DIR}" 2>/dev/null || true' EXIT
echo $$ > "${VC_LOCK_DIR}/pid" 2>/dev/null || true

if [ -n "${VERIFY_CI_GOMAXPROCS:-}" ]; then
  # Manual pin (existing override knob): value applies to -p, -parallel and
  # GOMAXPROCS alike; OOM kills will NOT downgrade below it.
  VC_P="${VERIFY_CI_GOMAXPROCS}"; VC_PARALLEL="${VERIFY_CI_GOMAXPROCS}"; VC_GOMAXPROCS="${VERIFY_CI_GOMAXPROCS}"
  VC_PINNED=1
  export GOMAXPROCS="${VC_GOMAXPROCS}"
  echo "[verify-ci] concurrency pinned by VERIFY_CI_GOMAXPROCS=${VC_GOMAXPROCS}"
else
  VC_PINNED=0
  pick_tier
fi

# Size GOMEMLIMIT from the environment's true memory budget when the caller
# did not pin it. Keep each worker comfortably BELOW the cgroup/host headroom:
# Go heap target near the ceiling guarantees an OOM kill, and margin must also
# absorb non-heap overhead (runtime stacks, mmapped binaries, page tables).
if [ -z "${VERIFY_CI_MEMLIMIT:-}" ]; then
  _mem_avail="$(effective_avail_mb 2>/dev/null || true)"
  case "${_mem_avail:-0}" in ''|*[!0-9]*) _mem_avail=0 ;; esac
  if [ "${_mem_avail}" -ge 8192 ]; then
    GOMEMLIMIT="2GiB"     # roomy: unchanged legacy cap
  elif [ "${_mem_avail}" -ge 4096 ]; then
    GOMEMLIMIT="1536MiB"
  elif [ "${_mem_avail}" -ge 2048 ]; then
    GOMEMLIMIT="1024MiB"
  else
    GOMEMLIMIT="768MiB"   # tight cgroup sandbox: stay well under the cap
  fi
  echo "[verify-ci] GOMEMLIMIT=${GOMEMLIMIT} (derived from avail=${_mem_avail:-?}MiB)"
  unset _mem_avail
fi

# run_with_oom_retry <desc> <cmd...>
# Runs cmd; if it is OOM-killed ("signal: killed" or exit 137), waits for the
# system memory pressure on shared machines to actually subside (polling
# available RAM, not a blind sleep), then retries — up to 5 attempts with
# escalating backoff. A bare OOM kill carries no code signal; real errors
# still fail immediately with the original exit code.
# 5 attempts (was 3): sustained pressure from concurrent agent workloads on
# shared machines can outlast 3 attempts x 3min waits; the larger budget only
# costs wall-clock under pressure and never changes the pass/fail verdict.
run_with_oom_retry() {
  local desc="$1"; shift
  local log status attempt backoff
  log="$(mktemp -t verifyci)"
  backoff=20
  for attempt in 1 2 3 4 5; do
    # Gate before EVERY attempt (incl. the first): on shared machines a
    # concurrent agent workload can spike memory between checks. Under
    # pressure the answer is DOWNGRADE AND GO, not wait - tier 1 needs only
    # ~1.5GiB, which fits almost any momentary state; stalling for pressure
    # to clear costs wall-clock the caller may not have. Cheap no-op when
    # memory is plentiful.
    if [ "${VERIFY_CI_SKIP_MEM_GATE:-0}" != "1" ]; then
      _avail="$(effective_avail_mb)"
      if [ -n "${_avail}" ] && [ "${_avail}" -lt "${VC_MEM_GATE_MB}" ]; then
        echo "[verify-ci] ${desc}: low available memory (${_avail}MiB < ${VC_MEM_GATE_MB}MiB) before attempt ${attempt}; downgrading concurrency and proceeding"
        if [ "${VC_PINNED:-0}" != "1" ]; then
          while [ "${VC_P}" -gt 1 ]; do downgrade_tier; done
        fi
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
      if [ "${attempt}" -eq 5 ]; then
        echo "[verify-ci] ${desc} was OOM-killed 5 times; giving up"
        cat "${log}"
        rm -f "${log}"
        return 1
      fi
      echo "[verify-ci] ${desc} was OOM-killed (attempt ${attempt}/5); downgrading concurrency and proceeding (avail=$(avail_mem_mb 2>/dev/null || echo '?')MiB)"
      if [ "${VC_PINNED:-0}" != "1" ]; then
        downgrade_tier
      fi
      wait_for_memory
      sleep "${backoff}"
      # Cap backoff growth at 60s: under sustained pressure the previous
      # 20->60->180->540s ladder alone burned ~15min of wall-clock, which a
      # caller-timed run pays for with its life.
      if [ "${backoff}" -lt 60 ]; then
        backoff=$((backoff * 3))
      fi
      continue
    fi
    cat "${log}"
    rm -f "${log}"
    return "${status}"
  done
  return 1
}

# Startup memory gate: on shared machines the run may start while memory
# pressure is already high. Forward progress beats waiting: downgrade to
# tier 1 immediately and go - tier 1 fits ~1.5GiB, so only a near-exhausted
# machine stalls at all, and only briefly (120s cap). Override with
# VERIFY_CI_SKIP_MEM_GATE=1.
if [ "${VERIFY_CI_SKIP_MEM_GATE:-0}" != "1" ]; then
  _avail="$(avail_mem_mb)"
  if [ -n "${_avail}" ] && [ "${_avail}" -lt "${VC_MEM_GATE_MB}" ]; then
    echo "[verify-ci] low available memory (${_avail}MiB < ${VC_MEM_GATE_MB}MiB); downgrading to tier 1 and proceeding"
    if [ "${VC_PINNED:-0}" != "1" ]; then
      while [ "${VC_P}" -gt 1 ]; do downgrade_tier; done
    fi
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
run_with_oom_retry "go build" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go build -p "${VC_P}" -tags goolm -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] running go vet"
# -p 1: vet defaults to -p=GOMAXPROCS; on shared/constrained runners the
# parallel type-checking of large packages (internal/agent, desktop) spikes
# peak RSS past GOMEMLIMIT and the process gets OOM-killed ("signal: killed")
# before any code issue is reported. Same rationale as the -p 1 on go test
# below. -p 2 still reproduced the OOM kill on shared machines, so 1 is the
# default; override via VERIFY_CI_VET_P.
run_with_oom_retry "go vet" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go vet -tags goolm -p "${VERIFY_CI_VET_P:-${VC_P}}" ./...

echo "[verify-ci] running tests (main module, unit only)"
# NOTE: do NOT use the "integration" tag here - integration tests (e.g. browser
# tests that spawn Chrome) are too heavy for CI and will OOM.
# -parallel 1: same rationale as the Makefile `test` target - packages using
# t.Parallel() still spike peak RSS when many test funcs run concurrently on
# memory-constrained machines; run one test func at a time.
#
# Chunked per top-level dir: a single `go test ./cmd/... ./internal/...` is one
# long serial retry unit; under sustained memory pressure from concurrent agent
# workloads on shared machines an OOM kill deep into the run burns the whole
# 3-attempt budget and verify-ci dies with a bare "signal: killed". Per-dir
# chunks shrink the retry unit - each chunk gets its own OOM budget and
# completed chunks are kept by the go test result cache, so a kill mid-suite
# only re-runs the affected chunk, not the whole 10+ minute serial pass.
test_chunks="./cmd"
# module path github.com/topcheer/ggcode/internal/<subdir> -> <subdir> is field 5
# Chunk discovery itself is OOM-retry-wrapped: the previously unwrapped
# `go list ./internal/...` loads the entire package graph and was the one
# heavy step left OUTSIDE any retry budget - under sustained memory pressure
# from concurrent agent workloads it died with a bare "signal: killed",
# aborting verify-ci before the protected test stages ever ran.
test_chunks_file="$(mktemp -t verifyci-chunks)"
if ! run_with_oom_retry "go list (test chunks)" \
	env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 \
	sh -c 'exec go list -tags goolm ./internal/... >"$1"' sh "${test_chunks_file}"; then
  rm -f "${test_chunks_file}"
  exit 1
fi
for _subdir in $(cut -d/ -f5 "${test_chunks_file}" | sort -u); do
  [ -n "${_subdir}" ] && test_chunks="${test_chunks} ./internal/${_subdir}"
done
rm -f "${test_chunks_file}"
for chunk in ${test_chunks}; do
  run_with_oom_retry "go test ${chunk}" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go test -tags goolm -p "${VC_P}" -parallel "${VC_PARALLEL}" -timeout 300s "${chunk}/..." || exit 1
done

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
