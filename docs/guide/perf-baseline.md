# Agent Performance Baseline & Regression Detection

ggcode tracks agent performance metrics across sessions and warns when efficiency regresses against historical baselines.

## What it does

1. **Records**: After each meaningful run (3+ tool calls or file edits), saves a compact summary to `.ggcode/perf-baseline.json` (rolling window of last 50 runs). Each entry is stamped with the **harness fingerprint** of the agent that produced it (system prompt + integrity checks + tool registry digest).
2. **Detects**: At the start of each new session, compares the last 3 runs against the median baseline of historical successful runs **recorded under the current harness**.
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
- **Harness-version gating**: Metrics from a different harness (changed system prompt, integrity checks, or tool set) are never compared against current runs — a baseline from a smaller harness would read as a false regression after ggcode gains new checks. Entries recorded before fingerprinting existed (no `hs` field) are excluded and age out of the 50-run window.

## Harness-version gating (sa-27)

Each run entry stores a `hs` fingerprint digest. Regression detection activates only once at least 5 historical runs share the current harness fingerprint; until then comparisons are skipped silently (visible in the debug ring under `perf-baseline`). This makes the detector robust to exactly the situation it otherwise misdiagnoses: the harness itself changed (new checks added, prompt edited, tools registered), which legitimately shifts iteration counts without any behavioral regression by the agent.

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
