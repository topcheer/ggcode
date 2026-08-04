# Run Efficiency Analysis

## Overview

ggcode analyzes each run's efficiency after completion and appends actionable
insights to the project memory reflection. This is a zero-LLM-cost, deterministic
analysis that identifies common anti-patterns in AI coding agent sessions.

## What It Detects

| Anti-Pattern | Trigger | Impact |
|---|---|---|
| Low edit-to-iteration ratio | <0.15 edits/iteration with 5+ iterations | Excessive exploration loops |
| Read amplification | 8+ reads, 0 edits, with errors | Trial-and-error cycles |
| High error rate | >40% of tool calls erroring | Wrong approach or missing prerequisites |
| Context pressure | >85% of context window used | Quality degradation from near-limit context |
| Compaction waste | 2+ compaction events in one run | Lost critical context from poor planning |

## How It Works

After each `RunStreamWithContent` call, `GenerateInsights()` calls
`AnalyzeEfficiency()` which inspects the accumulated `RunStats`:

1. Each anti-pattern deducts points from a baseline score of 100
2. The final score is clamped to 0-100
3. A level is assigned: Good (0 patterns), Fair (1), Poor (2+)
4. Recommendations are generated for each detected pattern

## Output Format

When anti-patterns are detected, an `[Efficiency Analysis]` section is appended
to the run reflection saved to project memory:

```
[Efficiency Analysis]
Score: 45/100 (Poor)
- High tool error rate (5/10 = 50%)
- Low edit-to-iteration ratio (1 edits / 20 iterations = 0.05)
Recommendations:
- After reading, form a concrete plan before attempting edits to avoid trial-and-error cycles.
- Check file existence and read files before editing to reduce edit failures.
```

Runs with no anti-patterns produce no output (no noise for efficient sessions).

## Integration

- **Source**: `internal/agent/efficiency_report.go`
- **Called from**: `GenerateInsights()` in `internal/agent/reflection.go`
- **Output destination**: Project memory via the reflection callback
- **Trigger**: After each meaningful run (3+ tool calls, file edits, or commands)

## Competitor Comparison

| Product | Session Analysis | Efficiency Metrics | Actionable Recommendations |
|---|---|---|---|
| Claude Code | Token usage only | No | No |
| Cursor | Token cost only | No | No |
| Devin | Post-session report | Partial | No |
| Aider | Diff stats only | No | No |
| **ggcode** | **Full reflection + efficiency** | **Yes** | **Yes** |
