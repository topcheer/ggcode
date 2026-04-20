#!/usr/bin/env bash
# Knight 一键自动化评估
# 用法:
#   ./scripts/dev/knight-eval.sh              # A/B 对比（baseline vs knight）
#   ./scripts/dev/knight-eval.sh --knight     # 仅 Knight 模式
#   ./scripts/dev/knight-eval.sh --baseline   # 仅 Baseline 模式
#   ./scripts/dev/knight-eval.sh --tasks task-01,task-02  # 指定任务
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "${repo_root}"

# ---- 颜色 ----
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}[eval]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; exit 1; }

# ---- 依赖检查 ----
check_deps() {
    local missing=0

    if ! command -v ggcode &>/dev/null; then
        fail "ggcode binary not found in PATH. Run 'make build' first."
    fi

    if ! command -v python3 &>/dev/null; then
        fail "python3 not found."
    fi

    # Check httpx
    if ! python3 -c "import httpx" 2>/dev/null; then
        warn "httpx not installed. Installing..."
        pip3 install httpx openai 2>/dev/null || pip install httpx openai 2>/dev/null
    fi

    ok "Dependencies checked."
}

# ---- 参数解析 ----
MODE_FLAG=""
EXTRA_ARGS=()
TASKS_ARG=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --knight)   MODE_FLAG="--mode knight"; shift ;;
        --baseline) MODE_FLAG="--mode baseline"; shift ;;
        --ab)       MODE_FLAG="--ab"; shift ;;
        --tasks)    TASKS_ARG="--tasks $2"; shift 2 ;;
        --no-llm)   EXTRA_ARGS+=("--no-llm"); shift ;;
        --llm-model) EXTRA_ARGS+=("--llm-model" "$2"); shift 2 ;;
        --output)   OUTPUT_DIR="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --knight       Run Knight-assisted mode only"
            echo "  --baseline     Run baseline mode only"
            echo "  --ab           Run A/B comparison (default)"
            echo "  --tasks IDS    Comma-separated task IDs"
            echo "  --no-llm       Skip LLM, use raw task descriptions"
            echo "  --llm-model M  Use specific LLM model"
            echo "  --output DIR   Output directory"
            echo "  -h, --help     Show this help"
            exit 0
            ;;
        *) warn "Unknown option: $1"; shift ;;
    esac
done

# Default: A/B comparison
if [[ -z "${MODE_FLAG}" ]]; then
    MODE_FLAG="--ab"
fi

timestamp="$(date +"%Y%m%d-%H%M%S")"
OUTPUT_DIR="${OUTPUT_DIR:-${repo_root}/.tmp/knight-eval-${timestamp}}"

# ---- 主流程 ----
echo ""
echo "========================================================"
echo "  Knight Automated Evaluation"
echo "========================================================"
echo "  Mode:      ${MODE_FLAG}"
echo "  Output:    ${OUTPUT_DIR}"
echo "  Timestamp: ${timestamp}"
echo "========================================================"
echo ""

check_deps

mkdir -p "${OUTPUT_DIR}"

# Run the Python orchestrator
info "Starting evaluation orchestrator..."

python3 "${repo_root}/scripts/eval/run_eval.py" \
    ${MODE_FLAG} \
    --auto \
    --output "${OUTPUT_DIR}" \
    --run-id "${timestamp}" \
    ${TASKS_ARG} \
    "${EXTRA_ARGS[@]}"

exit_code=$?

echo ""
echo "========================================================"
if [[ ${exit_code} -eq 0 ]]; then
    ok "Evaluation completed successfully!"
else
    fail "Evaluation exited with code ${exit_code}"
fi

echo ""
echo "Results:"
echo "  Output dir: ${OUTPUT_DIR}"

# Show scorecard if exists
for sc in "${OUTPUT_DIR}/ab-comparison.scorecard.md" \
         "${OUTPUT_DIR}/knight.scorecard.md" \
         "${OUTPUT_DIR}/baseline.scorecard.md"; do
    if [[ -f "${sc}" ]]; then
        echo ""
        echo "--- $(basename "${sc}") ---"
        cat "${sc}"
    fi
done

echo ""
info "Full results: ${OUTPUT_DIR}"
echo "========================================================"

exit ${exit_code}
