# Playbook Refinement: Degradation Demotion, Score-Aware Eviction, Staleness Decay

**Round:** r82-frontier
**Status:** implemented
**Concept source:** ACE — Agentic Context Engineering (Zhang et al., ICLR 2026, [arXiv:2510.04618](https://arxiv.org/abs/2510.04618)), "grow-and-refine" curation; context-rot background ([Chroma Research, 2025](https://www.trychroma.com/research/context-rot)).

## Problem

The strategy playbook (`internal/agent/playbook.go`, ACE-inspired) recorded successes
and ranked hints by `score = min(uses,10) × 10/avgIter`, but had no refine phase:

1. **No degradation demotion.** `AvgIter` is a cumulative average over all history.
   A strategy that used to finish in ~5 iterations but now needs ~20 (codebase grew,
   test suite slowed) keeps its historical score forever. Stale advice is injected
   into every system prompt — a context-rot tax that crowds out genuinely efficient
   patterns.
2. **Eviction was pure recency (LRU).** A slow 50-iteration strategy touched yesterday
   survived while a fast frequently-used strategy unused for a month was dropped —
   the opposite of what a strategy playbook should keep.
3. **No staleness weighting.** Entries that no longer describe how the workspace
   behaves occupied the 3-hint budget indefinitely.

## Design

All three fixes live in the read/score/curation path — no new injection path, no
change to the hint budget or prompt layout.

### 1. Degradation demotion (`updateEntry`)

`PlaybookEntry` gains `RecentIters` — a bounded ring of the last 3 per-run iteration
counts (optional JSON field, backward compatible). After each successful record, if
the entry has ≥2 ring observations and ≥3 historical uses, and the recent mean is
both **1.5× the historical average** and **at least 2 iterations worse**, the entry is
demoted:

- `Uses` halved (floor 1) — drops the frequency/confidence weight.
- `AvgIter` blended halfway toward the recent mean — the score tracks reality.

The entry is kept, not deleted: degradation may be workspace-transient, and the next
efficient run restores its rank. Three consecutive degraded runs are required in
practice because the ring must flush historical observations first — this makes
demotion robust against single-run outliers.

### 2. Score-aware eviction (`evict`)

Capacity eviction now removes the **lowest-score** entries (ties broken toward older)
instead of the oldest. This matches ACE's curator: remove the least useful strategies.

### 3. Staleness decay (`playbookScore` / `staleFactor`)

Score is multiplied by a freshness factor: `1.0` while fresh, halving every 30 days
past the last observation, floored at `0.25`. A zero `LastSeen` (legacy persisted
entries) is treated as fresh. Any new observation refreshes `LastSeen` and fully
restores the factor — decay affects ranking only, never data.

## Tests

`internal/agent/playbook_test.go`:

- `TestPlaybookDegradationDemotion` — 4 fast runs then 3 slow runs → Uses halved,
  AvgIter blended up, score drops.
- `TestPlaybookNoDemotionWhenHealthy` — consistent iterations → no demotion.
- `TestPlaybookEvictScoreAware` — a 50-iteration strategy is evicted in favor of
  fast strategies despite being most recently seen.
- `TestPlaybookScoreStalenessDecay` — 100-day-old entry ranks below an identical
  fresh one; refresh fully recovers.
