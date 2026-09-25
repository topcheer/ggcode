# Playbook / Harness Self-Evolution Observability (r84)

## Motivation

ggcode's harness self-evolution layer (built across r76-r83) actively shapes
every session: learned ratchet rules are injected into the system prompt
(`agent-rules.json`), and task strategy patterns are injected as hints
(`playbook.json`). Until r84 this layer was a black box to users:

- Playbook entries (`TaskType`, `Uses`, `SuccessRate`, `AvgIter`,
  `AvgDurationS`) had **no user-facing surface at all** — `/rules` listed
  rule text only.
- Learned rules had no health lens: nothing showed which rules had gone
  stale (>30 days without a hit), the category distribution, or how close
  the store was to its 60-rule capacity.

## Frontier anchor

Agent **evaluation & observability** is a first-class 2026 category
(VoltAgent's curated 2026 paper list; Google Cloud *AI Agent Trends 2026*
identifies agent observability/trust as a top enterprise concern). The
context-engineering literature (e.g. "Context Engineering: Offload,
Summarize, Isolate, Cache", 2026-03) stresses that learned/curated prompt
layers must remain inspectable to be governable — "human-in-the-loop harness
governance". This rounds off the r76-r83 arc by closing the read loop:
users can finally *see* what the self-evolution layer learned and whether
it is healthy.

## Design

Read-only by construction. The panel re-opens both stores from the working
directory on each render; it never writes, so it cannot race the agent's
writers.

- `internal/agent/playbook_observability.go`
  - `Playbook.Snapshot()` — mutex-safe copy accessor.
  - `FormatPlaybookDigest(entries, rules, now)` — pure, deterministic
    renderer (same input → byte-identical output). Sections:
    1. **Task strategies**: entries sorted by `Uses` (ties by recency),
       showing sequence/success rate/iterations/duration/age; capped at 12
       rows.
    2. **Learned rules health**: total vs capacity, stale count (>30d),
       category histogram, stalest-rule preview (up to 5, with hit counts
       and age).
- `internal/tui/playbook_panel.go` — `/playbook` panel following the
  established `statsPanel` pattern (`renderContextBox`, viewport, en/zh
  catalogs, close-table registration, resize sync).

`/playbook` is deliberately a *health* view; `/rules` remains the *content*
view (full rule text management).

## Testing

- `internal/agent/playbook_observability_test.go` — ordering, success-rate
  formatting, determinism, staleness detection, snapshot copy semantics,
  on-disk fixture load.
- `internal/tui/playbook_panel_test.go` — close-table invariant (the #2363
  pattern: every panel in `hasActivePanel` needs a `closeActivePanel` case)
  and the dir-parameterized body read path.

## Backlog candidates

- Wire playbook/rule health into autopilot run reports (`/runreport`).
- Surface per-rule injection counts (how often a rule actually reached the
  prompt) vs its `HitCount`.
