# Execution Trace: Span Tree & Waterfall Analysis

## Overview

ggcode's `/export-trace` command produces a structured JSON trace document containing the complete execution history of a session. As of this design, the trace document provides **three complementary views** of the same metric data:

1. **Flat event log** (`events[]`) - append-only list of every LLM call and tool execution
2. **Hierarchical span tree** (`span_tree`) - session → turn → llm/tool parent-child hierarchy
3. **Latency waterfall** (`waterfall[]`) - per-turn analysis with parallel groups, critical path, and bottleneck identification

This brings ggcode's observability to parity with industry-standard tracing tools (LangSmith, Langfuse, OpenTelemetry GenAI, Phoenix/Arize) that expect span-tree formats for visualization.

## Motivation

### Industry Trend (2025-2026)

Agent observability is the hottest infrastructure category for LLM applications:
- **LangSmith** (LangChain) - tracing, evaluation, datasets
- **Langfuse** ($10M+ funding) - open-source LLM observability
- **OpenTelemetry GenAI semantic conventions** - standardized span attributes for LLM calls
- **Phoenix/Arize** - LLM evaluation and tracing platform
- **LangGraph Studio** - visual trace inspection

The core abstraction across all these tools is the **span tree**: a hierarchical structure where each operation (LLM call, tool execution, sub-agent) becomes a span with parent-child relationships, timing, and attributes.

### Competitor Analysis

| Tool | Trace Format | Span Tree | Waterfall | Error Chain | Token/Cost |
|------|-------------|-----------|-----------|-------------|------------|
| Claude Code (`--debug trace`) | Flat log | No | No | No | Per-turn |
| Cursor (activity log) | Flat timeline | No | No | No | No |
| OpenHands/Cline (event stream) | Event stream | No | No | Partial | No |
| Aider (`/tokens`) | Token report | No | No | No | Yes |
| LangGraph Studio | Span tree | Yes | Yes | Yes | Yes |
| **ggcode** (this design) | **All three** | **Yes** | **Yes** | **Yes** | **Yes** |

### Gap Analysis

Prior to this implementation, ggcode had:
- Rich per-turn metrics (TTFT, think time, token usage, cache stats, tool latency) via `MetricEvent`
- A `/export-trace` command producing flat JSON event log
- `/stats` TUI panel with aggregated views

**Missing**:
- No hierarchical span tree (flat events only)
- No latency waterfall / critical-path analysis
- No parallel-vs-sequential tool analysis
- No bottleneck identification
- No error-span extraction for root-cause analysis

## Design

### Span Tree (`span_tree.go`)

The `SpanNode` struct follows OTel/LangSmith semantics:

```go
type SpanNode struct {
    SpanID     string                 // deterministic SHA-1 hash
    ParentID   string                 // parent span ID (empty for root)
    Name       string                 // human-readable name
    Kind       string                 // "session", "turn", "llm", "tool"
    StartTime  time.Time              // reconstructed from duration
    EndTime    time.Time              // event timestamp
    Attributes map[string]interface{} // tokens, cost, success, error
    Children   []SpanNode             // nested spans
}
```

**Hierarchy**: `session → turn → [llm, tool, tool, ...]`

**Deterministic IDs**: Span IDs are SHA-1 hashes of `parentID + kind + turnIndex + seqIdx`. This makes traces reproducible and diff-able across runs - critical for regression testing.

**Start-time reconstruction**: MetricEvents are emitted at completion (they carry Duration but not explicit start/end). We reconstruct: `start = timestamp - duration`. This requires zero additional instrumentation.

**Helper functions**:
- `CountSpans(node)` - total spans in tree
- `FlattenSpans(node)` - OTLP-style flat array with parent_id refs
- `FindErrorSpans(node)` - extract all failed tool spans for root-cause analysis

### Waterfall Analysis (`waterfall.go`)

The `WaterfallAnalysis` struct provides per-turn latency breakdown:

```go
type WaterfallAnalysis struct {
    TurnIndex             int           // which turn
    WallClock             time.Duration // earliest start to latest end
    TotalToolTime         time.Duration // sum of all tool durations
    LLMTime               time.Duration // sum of LLM durations
    OverlapRatio          float64       // 0=sequential, 1=fully parallel
    CriticalPathDuration  time.Duration // longest sequential chain
    BottleneckTool        string        // slowest single tool
    BottleneckDuration    time.Duration // slowest tool's duration
    ParallelToolGroups    [][]ToolSpan  // sets of overlapping tools
    SequentialChain       []ToolSpan    // critical path tools
}
```

**Key algorithms**:
- **Overlap ratio**: `(totalToolTime - mergedToolWallClock) / totalToolTime`. Detects parallel execution.
- **Parallel groups**: interval merging - tools whose `[start, end)` intervals intersect are grouped.
- **Critical path**: DP-based longest chain of non-overlapping intervals (minimum possible wall-clock time with perfect parallelization).
- **Bottleneck**: simple max-duration tool scan.

### Trace Document (`trace_export.go`)

The `TraceDocument` now embeds all three views:

```json
{
  "session_id": "abc123",
  "vendor": "anthropic",
  "model": "claude-sonnet-4",
  "summary": { ... },
  "events": [ ... ],
  "span_tree": { "kind": "session", "children": [ ... ] },
  "waterfall": [ { "turn_index": 1, "wall_clock_ms": ... } ],
  "error_spans": [ ... ]
}
```

## Implementation Notes

- **Zero LLM cost**: All analysis is deterministic interval arithmetic - no model calls.
- **Non-invasive**: Uses existing `MetricEvent` data already collected by the agent loop. No new instrumentation points.
- **Backward compatible**: New fields are added to `TraceDocument`; existing consumers of `events[]` and `summary` are unaffected.
- **Reproducible**: Deterministic span IDs enable trace diffing for regression analysis.

## Usage

```bash
# Export current session's trace (includes span tree + waterfall)
/export-trace

# Export a specific session by ID
/export-trace <session-id>
```

The output JSON can be:
- Parsed with `jq` for quick analysis
- Imported into Langfuse/LangSmith-compatible tools
- Diffed across sessions to detect latency regressions
