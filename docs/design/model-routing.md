# Task-Tier Model Routing (aux_model)

## Problem

An agent harness makes two classes of LLM calls:

1. **Primary loop calls** — conversation turns with tool schemas; need the
   full reasoning model.
2. **Auxiliary calls** — mechanical, bounded tasks:
   - context compaction summarization (`agent_compact.go` → `CheckAndSummarize`)
   - autopilot strategist guidance/classification (`autopilot_strategist.go`)
   - run health probe (`health_check.go`)

Before this change, every auxiliary call reused the primary provider and
model, so frontier-model pricing applied to summarization. The existing
`fallbacks` chain only switches models on FAILURE — it cannot route by task
tier (2026 "model routing" practice: route mechanical calls to the cheapest
capable model; 40-85% cost reduction with no visible quality loss).

## Design

- **Config**: top-level `aux_model: <name>` (optional). Must name a model
  served by the SAME resolved endpoint (same vendor / API key / protocol) —
  the common relay case where one endpoint fronts several model tiers.
- **Agent** (`internal/agent/model_routing.go`):
  - `SetAuxModel(resolved, model)` clones the resolved endpoint with
    `Model = aux_model` (no-op when empty or identical to the main model).
  - `auxProviderFor()` lazily builds and caches a second provider from the
    clone; construction failure logs once and permanently degrades to the
    main provider (routing must never break compaction/strategist
    availability).
  - `auxChat()` mirrors `provider.Chat` so call sites are drop-in swaps.
- **Routing points**: `auxProviderFor()` / `auxChat()` are used at exactly
  the three auxiliary call sites. The primary conversation loop always uses
  `a.provider`.
- **Wiring**: `SetAuxModel` is called in all three assembly paths
  (`root.go`, `pipe.go`, `daemon.go`), keeping them behaviorally aligned.

## Guarantees

- Opt-in: unset `aux_model` = byte-identical behavior to before.
- Protocol-safe: aux calls use the normal provider `Chat` API; nothing is
  inserted between tool_calls and tool_results.
- Fail-open: any aux provider problem falls back to the main model, never
  to a missing summarization/strategist result.
