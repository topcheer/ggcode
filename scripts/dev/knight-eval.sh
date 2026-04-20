#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

timestamp="$(date +"%Y%m%d-%H%M%S")"
output_dir="${1:-${repo_root}/.tmp/knight-eval-${timestamp}}"
mkdir -p "${output_dir}"

cat > "${output_dir}/README.md" <<EOF
# Knight evaluation bundle

- Created: ${timestamp}
- Repo: ${repo_root}

Files:
- baseline.log: output from CI-style and focused Knight regression checks
- metrics.csv: quantitative result sheet for A/B and soak runs
- scorecard.md: qualitative + acceptance checklist

Next steps:
1. Read docs/research/knight-evaluation-plan.md
2. Run the real-provider smoke / A-B / soak phases described there
3. Append results into metrics.csv and scorecard.md
EOF

cat > "${output_dir}/metrics.csv" <<'EOF'
run_id,phase,mode,provider_vendor,provider_endpoint,provider_model,task_set,task_id,success,error_count,tool_calls,turns,elapsed_sec,token_input,token_output,staged_skills,patched_skills,rollbacks,notes
EOF

cat > "${output_dir}/scorecard.md" <<'EOF'
# Knight scorecard

## Phase completion
- [ ] Phase 1 regression gate passed
- [ ] Phase 2 real-provider smoke passed
- [ ] Phase 3 A/B benchmark completed
- [ ] Phase 4 soak run completed

## Key observations
- Best improvement:
- Worst regression:
- Noisiest behavior:
- Most useful staged skill:
- Most suspicious staged skill:

## Go / no-go
- [ ] Safe for shadow mode
- [ ] Safe for staged autonomous use
- [ ] Needs more hardening before real background use
EOF

{
  echo "[knight-eval] output dir: ${output_dir}"
  echo "[knight-eval] running CI-style verification"
  ./scripts/dev/verify-ci.sh
  echo "[knight-eval] running focused Knight suites"
  go test ./internal/knight ./internal/tui ./cmd/ggcode
} | tee "${output_dir}/baseline.log"

echo
echo "[knight-eval] Baseline regression checks completed."
echo "[knight-eval] Bundle created at: ${output_dir}"
echo "[knight-eval] Next: follow docs/research/knight-evaluation-plan.md for real-provider smoke, A/B, and soak phases."
