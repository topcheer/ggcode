# Context Engineering Intelligence Gates

## Overview

This document describes two context engineering intelligence gates implemented in
ggcode's agent loop, following the principles outlined in Anthropic's Context
Engineering guide (2025) and the ACE benchmark (ICLR 2026).

Both gates are **zero-LLM-cost, deterministic** implementations that use token
arithmetic and rolling window analysis to detect suboptimal context patterns
and inject actionable guidance.

## 1. Prompt Cache Efficiency Monitor

**File**: `internal/agent/cache_efficiency_monitor.go`

### Problem

Anthropic's prompt caching uses a prefix-based cache with a 5-minute sliding TTL.
When the prefix (system prompt + early conversation + tool definitions) changes -
even by a single token - the entire cache is invalidated and the full prefix is
re-written at 1.25x cost on the next call.

Common cache-busting patterns in coding agents:
- **Dynamic tool pruning**: adding/removing tools mid-run busts the tools breakpoint
- **System prompt mutations**: intelligence gates injecting advisory text into the system prompt
- **Pinned context updates**: mid-conversation pin operations modify the prefix
- **Adaptive effort changes**: frequent effort level switches may affect cache stability

### What It Does

- Tracks `cache_read` / `input` token ratio per LLM call in a rolling 8-call window
- Detects "cache bust storms": consecutive cold calls (near 0% hit) after previously
  warm calls (50%+ hit)
- When 3+ consecutive cold calls follow warm calls, injects guidance identifying
  likely causes and fixes
- Fires at most once per run (avoids nagging)

### Differentiation

- **cache_keepalive.go**: keeps cache warm during IDLE periods (TTL management)
- **cache_efficiency_monitor.go** (this): detects cache INSTABILITY during ACTIVE runs

## 2. Context Window Pressure Forecaster

**File**: `internal/agent/context_pressure_forecast.go`

### Problem

Reactive compaction (triggered at 80% of context window) fires DURING an active
turn, abruptly truncating conversation history at an unpredictable point. This
can lose mid-task context that the agent was about to reference.

### What It Does

- Tracks token consumption rate per iteration using least-squares linear regression
- Estimates remaining iterations before the compaction threshold (80%) is hit
- When estimated iterations-to-compaction falls below threshold:
  - **Warning** (4 iterations): suggests wrapping up, using targeted tool calls
  - **Critical** (2 iterations): urgent, suggests avoiding broad searches
- Capped at 3 warnings per run with 3-minute cooldown between warnings

### Differentiation

- **budget.go**: analyzes WHAT is consuming context (category breakdown)
- **context_footprint.go**: identifies WHICH tools dominate (attribution)
- **agent_precompact.go**: performs background compaction (execution)
- **context_pressure_forecast.go** (this): predicts WHEN compaction is needed (timing)

## Competitor Analysis

| Feature | Claude Code | Cursor | Aider | OpenHands | ggcode |
|---------|------------|--------|-------|-----------|--------|
| Auto-compact | Reactive (95%) | Implicit | Manual /clear | Reactive | Reactive + Precompact |
| Cache efficiency monitoring | No | No | No | No | **Yes** |
| Context pressure forecasting | No | No | No | No | **Yes** |
| Context budget breakdown | Partial | No | /tokens | No | **Yes** |
| Per-tool footprint attribution | No | No | No | No | **Yes** |
| Pinned context | No | Yes | No | No | **Yes** |

## Integration Points

Both monitors are wired into the agent loop in `agent.go`:
1. **Struct fields**: `cacheEffMonitor` and `pressureForecaster` on Agent struct
2. **Initialization**: in `NewAgent()` alongside other intelligence state
3. **Reset**: in `RunStreamWithContent()` run-start reset block
4. **Recording**: after each LLM call, right after `emitUsage(resp.Usage)`
5. **Guidance injection**: via `contextManager.Add()` as user-role messages
