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
- **Model-scoped (sa-33)**: Each run is stamped with its model identity (`vendor/endpoint/model`, injected on every provider build and mid-session `/model` switch). Baseline medians and the 2/3 consensus vote only compare runs produced by the same model — iterations, duration, and context peak differ systematically across models, so without this a model switch would report expected cross-model variance as a regression. Until the new model accumulates 5+ of its own runs, regression detection stays silent. Baselines recorded before this change carry no model identity and remain comparable to everything (backward compatible).
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
