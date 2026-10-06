# SA-133: Speculative Execution & Agent Eval Frontier Scan

## Objective
Scan two frontier concept families (speculative tool execution; LLM-as-judge agent evaluation) against ggcode's ~191 existing detectors and identify implementable gaps.

## Methodology
- Online search: Anthropic engineering blog on speculative decoding/tool lookahead (2025-11-24), Berkeley Function-Calling Leaderboard, AgentBench/AgentBoard eval suites
- Novelty check against prior roadmap sessions r243/r244
- Live complexity measurement via code_health on fallback candidate

## Key Findings

### 1. Speculative Tool Execution / Lookahead
- Frontier idea: run predicted tool calls in parallel, verify cheaply, discard on mismatch.
- Status: ✅ **Already Implemented** — judged in roadmap session r243 (verifier-gated parallel execution exists); no new gap.
- Residual variant (cross-turn speculation reuse) adds protocol risk: inserting logic between `tool_calls` and `tool_results` is forbidden by our A2A/MCP compatibility constraint.

### 2. LLM-as-Judge / Agent Eval Harness
- Frontier idea: judge-rubric scoring of agent trajectories; online eval-driven routing.
- Status: ❌ **Rejected** in roadmap session r244 (risk > reward for this codebase; eval harness overlaps existing review_changes + causal attribution tests).
- Re-open condition: only if an external benchmark suite (BFCL-style) is adopted.

### Conclusion
**No novel frontier gap worth implementing.** Both scanned families are either already implemented (r243) or formally rejected (r244). Per constraints (no new detectors; prioritize refactoring), fallback to sanctioned refactor candidate below.

## Fallback: Sanctioned Refactor Candidate (measured this session)
`internal/tui/knight_commands.go` — verified live via code_health:

| Severity | CC | Function | Line | Length |
|----------|----|----------|------|--------|
| high | 25 | `*Model.handleKnightCommand` | :21 | 65 |
| medium | 14 | `*Model.knightReviewCmd` | :161 | 52 |
| medium | 12 | `*Model.knightProposalsCmd` | :283 | 55 |

Health 86/100, avg CC 6.5, max 25 — `handleKnightCommand` is the dominant hotspot (dispatch switch over knight subcommands).

Recommended split: extract per-subcommand handlers (knight review/proposals/etc. already exist as separate methods — the dispatcher mainly needs a command→handler map), targeting CC ≤ 8. No API change; pure internal refactor of tui package.

## Sources
- Anthropic engineering blog: building blocks of speculative execution (2025-11-24)
- Berkeley Function-Calling Leaderboard (BFCL) — agent eval taxonomy
- AgentBench / AgentBoard — trajectory evaluation suites
