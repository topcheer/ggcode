#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

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

if [ -n "${VERIFY_CI_MEMLIMIT:-}" ]; then
  GOMEMLIMIT="${VERIFY_CI_MEMLIMIT}"
else

  GOMEMLIMIT="2GiB"
fi

FULL="${VERIFY_CI_FULL:-0}"

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

cgroup_free_mb() {
  local lim cur free_bytes

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

wait_for_memory() {
  local max_wait="${1:-120}"
  local waited=0 avail
  while [ "${waited}" -lt "${max_wait}" ]; do
    avail="$(effective_avail_mb)"
    if [ -z "${avail}" ] || [ "${avail}" -ge 1500 ]; then
      return 0
    fi
    sleep 10
    waited=$((waited + 10))
  done
  return 0
}

VC_P=1; VC_PARALLEL=1; VC_GOMAXPROCS=1; VC_MEM_GATE_MB=1500
vc_load_1m() {

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

downgrade_tier() {
  if [ "${VC_P}" -gt 1 ]; then
    VC_P=$((VC_P / 2)); VC_PARALLEL=$((VC_PARALLEL / 2)); VC_GOMAXPROCS=$((VC_GOMAXPROCS / 2))
    export GOMAXPROCS="${VC_GOMAXPROCS}"
    echo "[verify-ci] concurrency downgraded to -p ${VC_P} -parallel ${VC_PARALLEL} GOMAXPROCS=${VC_GOMAXPROCS}"
  fi
}

VC_VERDICT_FILE="${TMPDIR:-/tmp}/ggcode-verify-ci.verdict"
VC_VERDICT_TTL=1800
vc_tree_fingerprint() {
  {
    git rev-parse HEAD 2>/dev/null || echo no-head
    git diff HEAD 2>/dev/null

    git ls-files --others --exclude-standard 2>/dev/null |
      while IFS= read -r f; do
        [ -f "${f}" ] && git hash-object "${f}" 2>/dev/null
      done
    echo "FULL=${FULL}"
  } | git hash-object --stdin 2>/dev/null
}
vc_write_verdict() {
  local fp; fp="$(vc_tree_fingerprint)"
  [ -n "${fp}" ] && printf '%s %s\n' "${fp}" "$(date +%s)" > "${VC_VERDICT_FILE}" 2>/dev/null || true
}
if [ "${VERIFY_CI_NO_SHORTCUT:-0}" != "1" ]; then
  _fp="$(vc_tree_fingerprint)"
  if [ -n "${_fp}" ] && [ -r "${VC_VERDICT_FILE}" ]; then
    _cached_fp=""; _cached_ts=0
    read -r _cached_fp _cached_ts < "${VC_VERDICT_FILE}" 2>/dev/null || true
    _now=$(( $(date +%s) ))
    if [ "${_cached_fp}" = "${_fp}" ] && [ "$((_now - _cached_ts))" -lt "${VC_VERDICT_TTL}" ]; then
      echo "[verify-ci] recent green verdict for identical tree ($((_now - _cached_ts))s ago) - short-circuit pass"
      exit 0
    fi
  fi
fi
unset _fp _cached_fp _cached_ts _now 2>/dev/null || true

VC_LOCK_DIR="${TMPDIR:-/tmp}/ggcode-verify-ci.lock"
VC_LOCK_WAITED=0
while ! mkdir "${VC_LOCK_DIR}" 2>/dev/null; do

  _lock_pid="$(cat "${VC_LOCK_DIR}/pid" 2>/dev/null || true)"
  if [ -n "${_lock_pid}" ] && ! kill -0 "${_lock_pid}" 2>/dev/null; then
    echo "[verify-ci] removing stale verify-ci lock from dead pid ${_lock_pid}"
    rm -rf "${VC_LOCK_DIR}"
    continue
  fi

  if [ -z "${_lock_pid}" ]; then
    _lock_age=$(( $(date +%s) - $(stat -f %m "${VC_LOCK_DIR}" 2>/dev/null || stat -c %Y "${VC_LOCK_DIR}" 2>/dev/null || echo 0) ))
    if [ "${_lock_age}" -ge 30 ]; then
      echo "[verify-ci] removing orphan verify-ci lock (no pid file, age ${_lock_age}s)"
      rm -rf "${VC_LOCK_DIR}"
      continue
    fi
  fi
  if [ "${VC_LOCK_WAITED}" -ge 90 ]; then
    echo "[verify-ci] lock still held after 90s; proceeding without serialization"
    break
  fi
  if [ "${VC_LOCK_WAITED}" -eq 0 ]; then
    echo "[verify-ci] another verify-ci run is active; waiting for it to finish (up to 90s)"
  fi
  sleep 10
  VC_LOCK_WAITED=$((VC_LOCK_WAITED + 10))
done
trap 'rm -rf "${VC_LOCK_DIR}" 2>/dev/null || true' EXIT
echo $$ > "${VC_LOCK_DIR}/pid" 2>/dev/null || true

if [ "${VERIFY_CI_NO_TEE:-0}" != "1" ]; then
  _tee_log="${TMPDIR:-/tmp}/verify-ci-$(date +%Y%m%d-%H%M%S).log"
  echo "[verify-ci] full log: ${_tee_log}"
  exec > >(tee "${_tee_log}") 2>&1
fi

if [ -n "${VERIFY_CI_GOMAXPROCS:-}" ]; then

  VC_P="${VERIFY_CI_GOMAXPROCS}"; VC_PARALLEL="${VERIFY_CI_GOMAXPROCS}"; VC_GOMAXPROCS="${VERIFY_CI_GOMAXPROCS}"
  VC_PINNED=1
  export GOMAXPROCS="${VC_GOMAXPROCS}"
  echo "[verify-ci] concurrency pinned by VERIFY_CI_GOMAXPROCS=${VC_GOMAXPROCS}"
else
  VC_PINNED=0
  pick_tier
fi

if [ -z "${VERIFY_CI_MEMLIMIT:-}" ]; then
  _mem_avail="$(effective_avail_mb 2>/dev/null || true)"
  case "${_mem_avail:-0}" in ''|*[!0-9]*) _mem_avail=0 ;; esac
  if [ "${_mem_avail}" -ge 8192 ]; then
    GOMEMLIMIT="2GiB"
  elif [ "${_mem_avail}" -ge 4096 ]; then
    GOMEMLIMIT="1536MiB"
  elif [ "${_mem_avail}" -ge 2048 ]; then
    GOMEMLIMIT="1024MiB"
  else
    GOMEMLIMIT="768MiB"
  fi
  echo "[verify-ci] GOMEMLIMIT=${GOMEMLIMIT} (derived from avail=${_mem_avail:-?}MiB)"
  unset _mem_avail
fi

run_with_oom_retry() {
  local desc="$1"; shift
  local log status attempt backoff
  log="$(mktemp -t verifyci)"
  backoff=20
  for attempt in 1 2 3 4 5; do

    if [ "${VERIFY_CI_SKIP_MEM_GATE:-0}" != "1" ]; then
      _avail="$(effective_avail_mb)"
      if [ -n "${_avail}" ] && [ "${_avail}" -lt "${VC_MEM_GATE_MB}" ]; then
        if [ "${VC_PINNED:-0}" != "1" ] && [ "${VC_P}" -gt 1 ]; then
          echo "[verify-ci] ${desc}: low available memory (${_avail}MiB < ${VC_MEM_GATE_MB}MiB) before attempt ${attempt}; downgrading concurrency and proceeding"
          while [ "${VC_P}" -gt 1 ]; do downgrade_tier; done
          wait_for_memory 120
        else
          echo "[verify-ci] ${desc}: low available memory (${_avail}MiB < ${VC_MEM_GATE_MB}MiB) at tier 1 before attempt ${attempt}; waiting up to 10min for the storm to pass"
          wait_for_memory 600
        fi
      fi
    fi
    if "$@" >"${log}" 2>&1; then
      cat "${log}"
      rm -f "${log}"
      return 0
    fi
    status=$?

    echo "[verify-ci] ${desc}: attempt ${attempt} failed, rc=${status}"
    if grep -q "signal: killed" "${log}" || [ "${status}" -eq 137 ]; then
      if [ "${attempt}" -eq 5 ]; then
        echo "[verify-ci] ${desc} was OOM-killed 5 times; giving up"
        cat "${log}"
        rm -f "${log}"
        return 1
      fi
      echo "[verify-ci] ${desc} was OOM-killed (attempt ${attempt}/5); downgrading concurrency (avail=$(avail_mem_mb 2>/dev/null || echo '?')MiB)"
      if [ "${VC_PINNED:-0}" != "1" ]; then
        downgrade_tier
      fi

      if [ "${VC_P}" -eq 1 ]; then
        wait_for_memory 600
      else
        wait_for_memory 120
      fi
      sleep "${backoff}"

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

if [ "${VERIFY_CI_SKIP_MEM_GATE:-0}" != "1" ]; then

  _avail="$(effective_avail_mb)"
  if [ -n "${_avail}" ] && [ "${_avail}" -lt "${VC_MEM_GATE_MB}" ]; then
    echo "[verify-ci] low available memory (${_avail}MiB < ${VC_MEM_GATE_MB}MiB); downgrading to tier 1"
    if [ "${VC_PINNED:-0}" != "1" ]; then
      while [ "${VC_P}" -gt 1 ]; do downgrade_tier; done
    fi
    wait_for_memory 600
  fi
fi

echo "[verify-ci] checking gofmt cleanliness"
if ! test -z "$(gofmt -l ./cmd ./internal)"; then
  echo "[verify-ci] gofmt found unformatted files:"
  gofmt -l ./cmd ./internal
  exit 1
fi

echo "[verify-ci] downloading modules"

run_with_oom_retry "go mod download" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go mod download

echo "[verify-ci] building ggcode"

run_with_oom_retry "go build" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go build -p "${VC_P}" -tags goolm -o /tmp/ggcode ./cmd/ggcode

echo "[verify-ci] running go vet"

run_with_oom_retry "go vet" env GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go vet -tags goolm -p "${VERIFY_CI_VET_P:-${VC_P}}" ./...

echo "[verify-ci] running goroutine protection check"

go run ./scripts/dev/goroutine_check.go ./internal ./cmd ./desktop

echo "[verify-ci] running tests (main module, unit only)"

test_chunks="./cmd"

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
vc_write_verdict

if [ "${FULL}" = "1" ]; then
  echo ""
  echo "[verify-ci:full] cross-platform compile check (linux + windows)"
  for target in "linux/amd64" "windows/amd64"; do
    os="${target%%/*}"
    arch="${target##*/}"

    if ! run_with_oom_retry "cross-compile ${os}/${arch}" env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOMEMLIMIT="${GOMEMLIMIT}" GOGC=50 go build -tags goolm ./cmd/ggcode; then
      echo "[verify-ci:full] cross-compile FAILED for ${os}/${arch}"
      exit 1
    fi
  done
  echo "[verify-ci:full] cross-platform compile check passed"

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

  if command -v python3 >/dev/null 2>&1; then
    python3 scripts/eval/validate_tasks.py || exit 1
    echo "[verify-ci:eval] eval harness integrity passed"
  else
    echo "[verify-ci:eval] WARNING: python3 not found, skipping eval-check"
  fi
  echo ""
  echo "[verify-ci:full] all checks passed"
  vc_write_verdict
fi
