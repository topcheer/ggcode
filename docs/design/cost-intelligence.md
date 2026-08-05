# Cost Intelligence: Cache Efficiency Analysis

## Overview

Cost Intelligence is the systematic tracking, analysis, and optimization of
LLM token costs. As token costs are the primary operating expense for AI
applications, cost optimization is a key differentiator.

This document covers the **Cache Efficiency Analysis** feature, which answers
a critical question: *"Is prompt caching actually saving money?"*

## Background

### Prompt Caching Economics

Prompt caching (Anthropic, Google Gemini context caching, OpenAI automatic
caching) trades higher write cost for dramatically lower read cost:

| Token Type     | Typical Price (relative to input) |
|----------------|-----------------------------------|
| Input          | 1.00x                             |
| Cache write    | 1.25x                             |
| Cache read     | 0.10x (90% savings)               |
| Output         | varies                            |

If the agent writes a lot to cache but rarely reads back (**cache thrashing**),
caching actually **loses** money compared to not caching at all.

### Competitor Analysis

| Product      | Cache Analysis                          |
|--------------|-----------------------------------------|
| Helicone     | Cache hit rate dashboard + savings calc |
| Braintrust   | Cost-per-call with cache attribution    |
| Claude Code  | None                                    |
| Cursor       | None                                    |
| Aider        | /tokens (raw counts only)               |
| OpenHands    | Basic cost tracking                     |
| Devin        | ACU credit system (no cache analysis)   |

**Gap**: No major AI coding agent detects cache thrashing or computes cache
savings at runtime.

## Implementation

### `internal/cost/cache_analysis.go`

The `CacheAnalysis` struct computes:

- **GrossSavingsUSD**: savings from cache reads vs full input price
- **CacheWritePremiumUSD**: extra cost paid for writing to cache
- **NetSavingsUSD**: GrossSavings minus WritePremium (negative = thrashing)
- **CacheReadRatio**: fraction of reads served from cache
- **WriteToReadRatio**: CacheWrite / CacheRead (>10 = thrashing)
- **PercentSaved**: overall cost reduction from caching
- **EfficiencyLevel**: coarse category (None/Excellent/Good/Marginal/Thrashing)

Two entry points:
- `Tracker.AnalyzeCache()` -- for live trackers
- `AnalyzeCacheFromSessionCost(sc, pricing)` -- for display layers with only
  aggregated data (e.g., `/cost` command)

### `internal/agent/cache_eff_gate.go`

Agent-level gate that detects cumulative cache thrashing during active runs.
Uses incremental write-to-read ratio (more sensitive to recent behavior than
cumulative). Fires at most once per run with targeted guidance.

**Threshold**: Write-to-read ratio > 10x (at typical 1.25x write / 0.10x read
pricing, this means caching is net-negative).

This complements the existing `cache_efficiency_monitor.go` which detects
per-call cache bust storms via a rolling window.

### `/cost` Command Integration

The `/cost` slash command now displays a Cache Efficiency section showing
aggregate savings, hit ratio, and thrashing warnings.

## Default Cache Pricing

When a metered model has no explicit cache rates in the pricing table, the
analysis defaults to industry-standard ratios:
- Cache read = 0.10 x input rate
- Cache write = 1.25 x input rate

These follow the Anthropic prompt caching pricing model used by most providers.

## Related Features

- **cache_keepalive.go**: keeps cache warm during idle periods (TTL-based)
- **cache_efficiency_monitor.go**: detects cache bust storms (prefix instability)
- **cache_eff_gate.go** (this): detects financial cache thrashing (write/read ratio)
- **budget_guard.go**: BAGEN-inspired per-step token cost trend monitoring
- **sub-agent-cost-isolation**: per-agent cost attribution
