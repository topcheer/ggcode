# Adaptive Tool Timeout

## Problem

The previous implementation used a flat **5-minute timeout** for ALL tool calls. This is suboptimal:

- A hung `read_file` or `grep` wastes 5 full minutes before the timeout fires
- A `web_fetch` or MCP tool that legitimately needs 90s could be killed prematurely if a lower flat timeout were used
- All tool categories (file I/O, search, LSP, web, MCP, browser) get the same timeout despite very different latency profiles

## Solution

Two-tier adaptive timeout computation that replaces the flat constant:

### 1. Category-Based Defaults

Tools are classified into categories with sensible per-category timeout defaults:

| Category | Tools | Default Timeout |
|----------|-------|----------------|
| File I/O | `read_file`, `multi_file_read`, `list_directory` | 60s |
| Search | `grep`, `search_files`, `glob`, `code_search` | 60s |
| Edit | `edit_file`, `write_file`, `multi_edit_file`, `multi_file_write`, `notebook_edit` | 30s |
| LSP | `lsp_*`, `code_health` | 60s |
| Web | `web_search`, `web_fetch` | 120s |
| MCP | `mcp__*` | 120s |
| Git | `git_*` | 60s |
| Browser | `browser`, `screenshot`, `mobile_device` | 180s |
| Default | (everything else) | 120s |

### 2. Latency-Adaptive Computation

When the existing `LatencyTracker` has accumulated 3+ samples for a tool, the timeout is computed as:

```
timeout = clamp(mean_latency * 5x, category_default, ceiling)
```

- **Floor**: 10s (allows for cold starts and variance)
- **Ceiling**: 5 minutes (same as old flat default — hard upper bound)
- **Category default as lower bound**: The adaptive timeout is never set below the category default unless at the floor, preventing premature kills from limited data

### Integration with LatencyTracker

The `LatencyTracker` (previously used only for outlier warning generation) now serves double duty:

1. **Outlier detection** (existing): warns the agent when a tool call is dramatically slower than its baseline
2. **Adaptive timeout** (new): uses the same latency baseline to compute per-tool timeout

This is a zero-waste integration: the latency data was already being collected. The `RecordAndCheck` method was refactored to record latency for ALL tools (not just the `latencyMonitoredTools` set), while still only generating outlier warnings for monitored tools.

## Competitor Analysis

| Agent | Per-Tool Timeout | Adaptive? |
|-------|-----------------|-----------|
| Claude Code | Flat 2-min | No |
| Cursor | None (session-level only) | No |
| Cline | 60s default, configurable per-type | Semi (manual config) |
| OpenHands | 300s flat | No |
| Aider | No per-tool timeout | No |
| **ggcode** | Category-based + latency-adaptive | **Yes** |

## Files

- `adaptive_timeout.go` — Category classification, default timeout table, and adaptive computation logic
- `agent_tool.go` — `executeToolWithTimeout` wired to use `computeAdaptiveTimeout()` instead of flat `defaultToolTimeout`
- `tool_latency.go` — `RecordAndCheck` refactored to record latency for all tools (not just monitored ones)
