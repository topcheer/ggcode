# Output-Flow Speculation (SA-133)

**Date:** 2026-09-22
**Branch:** `feat/output-flow-speculation`
**Commit:** 22f134ab

## Research (online sources)

1. **Speculative Actions: A Lossless Framework for Faster Agentic Systems**
   arXiv:2510.04371 (Oct 2025, rev. Apr 2026) — https://arxiv.org/abs/2510.04371
   Up to 55% next-action prediction accuracy → up to 20% latency reduction; lossless
   because actions commit only when predictions match. Confirms speculative execution
   as a mainstream 2025-2026 agent-harness technique.
2. **Act While Thinking: Pattern-Aware Speculative Tool Execution** (2026)
   https://www.researchgate.net/publication/402860178 — same family: exploit the
   LLM generation window to pre-execute predicted read-only calls.
3. **AutoTool: Efficient Tool Selection for LLM Agents** (AAAI 2026, arXiv:2511.14650)
   — already tracked in `docs/research/rotation-frontier-papers-2026-07-14.md`:
   Tool Inertia Graph = sequence edges (ggcode has) + **parameter-dependency edges
   (data flow between tools — ggcode lacked)**, 1.6x token-in speedup.

## Gap (user-perceivable)

ggcode's speculator (`internal/agent/speculate.go`) predicted next calls via bigram
sequences, but `predictArgs` could only inherit paths from the previous tool's INPUT
arguments (edit→read reuse). The most frequent agent sequence — search
(grep/code_search/glob/list_directory/lsp_references) → read_file of a hit — could
NEVER be speculated: the read target lives in the search tool's OUTPUT. Every such
read paid full tool-execution latency.

## Implementation

- `recordOutput()`: captures secret-redacted output of the last successful
  search-shaped call (32KB cap); cleared by non-source tools, errors, `resetSequence`.
- `extractOutputFlowPaths()`: extracts path candidates from grep content mode
  (`path.go:12: text`), lsp position mode (`path.go:12:5 ctx`), and bare listings;
  ranked by mention density (first-seen tiebreak); extension allowlist + lexical
  filters reject URLs/flags/junk; bounded at 64 distinct.
- `maybeSpeculateFromOutput()`: pre-reads ≤2 top candidates during the generation
  window; stat-validates each (exists, regular file, ≤512KB) before I/O; gated on ≥1
  observed bigram demonstration of searchTool→read_file.
- `launchSpeculation()`: shared launcher enforcing the global 3-goroutine cap;
  debug lines tagged bigram vs output-flow.

Safety: read-only targets only; wrong guesses expire via existing TTL + #1831
stat-snapshot freshness; differing model args are plain cache misses.

## Tests

`internal/agent/speculate_output_flow_test.go` (251 lines): store/clear/truncate of
output evidence; extraction ranking + URL rejection; end-to-end speculative pre-read
against a registry mock; gates (no evidence / non-source tool / empty output /
missing path); size-cap skip + candidate limit; reset hygiene.
Full `internal/agent` package passes; `go build`/`go vet`/gofmt clean (`-tags goolm`).

## Not touched

In-flight branches (feat/env-probe-memory-curation, fix/issue-2615/2619/2620,
feat/opencode-oauth, feat/auto-tool-distillation, feat/traj-learning-recall) and the
protected `internal/memory` probe/staleness/health_report files.
