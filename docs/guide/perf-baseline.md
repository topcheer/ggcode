# Agent Performance Baseline & Regression Detection

ggcode tracks agent performance metrics across sessions and warns when efficiency regresses against historical baselines.

## What it does

1. **Records**: After each meaningful run (3+ tool calls or file edits), saves a compact summary to `.ggcode/perf-baseline.json` (rolling window of last 50 runs).
2. **Detects**: At the start of each new session, compares the last 3 runs against the median baseline of all historical successful runs.
3. **Warns**: If 2+ of the last 3 runs show regression on the same metric (iterations, duration, error rate, context usage, or compaction), injects a concise advisory so the agent adjusts its strategy.

## Tracked metrics

| Metric | Regression Factor | Notes |
|--------|------------------|-------|
| Iterations | 1.5x baseline median | Too many iterations = unfocused work |
| Duration | 1.5x baseline median | Longer runs = unnecessary rework |
| Error rate | 2x baseline or >5% from 0% | High errors = misjudging tool args |
| Context peak | 1.5x baseline median | Context bloat reduces quality |
| Compaction | 3+ events from 0 baseline | Context too large for the task |

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
| OpenHands | Eval harnesses for benchmarking, not live detection |
| Aider | No |
| **ggcode** | **Yes** |

## How to disable

Delete `.ggcode/perf-baseline.json` or set the file to read-only.
