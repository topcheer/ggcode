# Agent Performance Baseline & Regression Detection

ggcode tracks agent performance metrics across sessions and warns when efficiency regresses against historical baselines.

## What it does

1. **Records**: After each meaningful run (3+ tool calls or file edits), saves a compact summary to `.ggcode/perf-baseline.json` (rolling window of last 50 runs).
2. **Detects**: At the start of each new session, compares the last 3 runs against the median baseline of all historical successful runs.
3. **Warns**: If 2+ of the last 3 runs show regression on the same metric (iterations, duration, error rate, context usage, or compaction), injects a concise advisory so the agent adjusts its strategy.

## Tracked metrics

| Metric | Regression Factor | Notes |
|--------|------------------|-------|
| Iterations | 1.5x per tool call | Too many turns per unit of work = chatty, unfocused work |
| Duration | 1.5x per tool call | High seconds-per-call = unnecessary rework or slow tools |
| Error rate | 2x baseline or >5% from 0% | High errors = misjudging tool args |
| Context peak | 1.5x per tool call | Context bloat reduces quality |
| Compaction | 3+ events from 0 baseline | Context too large for the task |

## Workload normalization

Iterations, duration, and context peak scale with task size: a deep-research
session legitimately peaks far above a median blended from quick-fix runs.
Comparing raw totals would flag every large task as a regression — a false
positive that itself pollutes the context it warns about.

When both the baseline and the compared run carry at least 5 tool calls,
these three metrics are compared **per tool call** instead (tokens/call,
seconds/call, iterations/call). This measures efficiency at any task size: a
367k-token peak across 110 tool calls stays silent next to a 174k-token /
60-call median, while genuine bloat (2x tokens for the same workload) still
fires. Advisories quote the per-call rate plus the run totals for context.
Below the 5-call floor, the legacy absolute comparison applies.

## Design principles

- **Zero LLM cost**: Deterministic statistical comparison (median-based)
- **Sustained regression only**: Requires 2/3 recent runs to be worse (avoids outlier false positives)
- **Median over mean**: Robust against outliers from unusually long/short tasks
- **Successful runs only**: Baseline computed from successful runs to avoid skewing by failures
- **Advisory, not blocking**: The warning helps the agent adjust strategy; it doesn't prevent work

## Competitor comparison

| Product | Cross-session regression detection |
|---------|----------------------------------|
| Claude Code | No (shows /cost per session only) |
| Cursor | No |
| Devin | Internal SICA tracking, no user-visible alerts |
| Aider | No |
| **ggcode** | **Yes** |

## How to disable

Delete `.ggcode/perf-baseline.json` or set the file to read-only.
