# Stale Heuristic Detection

ggcode ships ~100 built-in detectors that encode assumptions about model behavior (error rushing, attention fragmentation, edit oscillation, ...). Like all harness assumptions, these can go stale as models improve: a detector written to counter one model's quirk may never fire on the next model, where it is pure dead weight. (See Anthropic's "Scaling Managed Agents" post for the canonical "context anxiety" example: a workaround that a newer model made obsolete.)

## What it does

Every run, detector guidance firings are recorded per model to `.ggcode/memory/guidance-stats.jsonl` (see the [r402 guidance stats] observability layer):

```json
{"ts":"2026-10-05T12:00:00Z","model":"glm-5.2","tag":"Attention Fragmentation","delivered":1,"suppressed":0}
```

At the end of each run, a cross-model analysis appends `stale_heuristic` entries:

```json
{"type":"stale_heuristic","ts":"2026-10-05T12:05:00Z","model":"glm-5.3","tag":"Attention Fragmentation"}
```

A tag is reported stale for a model when **all** of the following hold:

- The model has at least **10 recorded runs**.
- The tag was **never delivered** in any of those runs.
- The tag is **still delivering (>= 2 times) for at least one other model** in the same project.

The cross-model contrast is what separates "model behavior changed" from "this project never trips this detector" — a docs workspace will not fire build-related detectors on *any* model, so nothing is reported.

## Design notes

- **Visibility only.** Stale heuristics are reported, never auto-disabled. Disabling a detector needs stronger evidence than absence of firing (the r402 "premature before misfire data exists" philosophy).
- **No static registry.** Detector headings change over time; the analysis is purely data-driven from the JSONL, so it never rots.
- **Legacy compatibility.** Records written before the `model` field existed are parsed but excluded from contrast — they may be the current model's own history.
- **Dedup.** Each (model, tag) pair is reported at most once until stats history is cleaned.

## Reading the results

Since r14/r15 the `/guidance` TUI command surfaces all of this without hand-grepping:

- `/guidance` — per-tag fire/suppress aggregates, per-model split, gated-detector state, and a trailing "stale (harness-flagged)" list (the `stale_heuristic` reports below).
- `/guidance <tag>` — drill down to the exact hint text last delivered for that tag (from `.ggcode/memory/guidance-hints.jsonl`).

Raw fallback (pre-r14 workspaces):

```sh
grep stale_heuristic .ggcode/memory/guidance-stats.jsonl | tail -20
```

Or with `GGCODE_DEBUG=1`, each report is also logged under the `guidance-stale` category.

## Related

- `internal/agent/guidance_stats.go` — per-run firing stats (r402)
- `internal/agent/guidance_stale.go` — the cross-model analyzer (r22)
