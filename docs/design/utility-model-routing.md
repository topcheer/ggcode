# Utility Model Routing (r93)

Date: 2026-09-26
Status: implemented

## Motivation

2026 frontier thinking on agent harnesses (OpenDev, "Building AI Coding
Agents for the Terminal", arXiv:2603.05344; Zaharia et al., "compound AI
systems"; RouteLLM-style model routing) frames agent capability as a
**system** property: different cognitive workloads should bind to different
models, trading cost/latency against capability per workflow. OpenDev's
first listed contribution is exactly this: *per-workflow LLM
configurability*.

ggcode routed **every** LLM call — including two purely auxiliary,
non-conversational workloads — through the single primary (frontier) model:

1. **Autopilot strategist** (`internal/agent/autopilot_strategist.go`): a
   next-action reasoning pass, up to `maxAutopilotStrategistCalls` (100)
   times per Run, each consuming frontier-model input tokens.
2. **Reactive compaction summarization**
   (`internal/agent/agent_compact.go`, `CheckAndSummarize`): periodic
   whole-conversation summaries, again on the frontier model.

Meanwhile the config layer already carried a `SmallModel` concept
(`vendorDefaultModels` in `internal/config/vendor_defaults.go`) that had
**zero consumers** — the routing hook existed in data but was never wired.

## Design

- New config key `utility_model` (top-level, `internal/config.Config`).
- Resolution (`internal/agentruntime/utility_route.go`):
  1. `cfg.UtilityModel` (explicit)
  2. `config.VendorSmallModelDefault(cfg.Vendor)` (built-in vendor default;
     empty for every current vendor, so no behavior change by default)
  3. `""` → no routing (historical behavior)
- `ResolveUtilityProvider` clones the active `ResolvedEndpoint`, overrides
  only `Model`, and builds a second provider. Skips (returns nil):
  unset model, model == primary, missing credentials. All failures are
  best-effort and logged; utility work falls back to the primary provider.
- `Agent.SetUtilityProvider` / `Agent.utilityProviderForWork`:
  auxiliary call sites (`strategist` Chat, `CheckAndSummarize`) route
  through the helper; nil utility provider ⇒ primary provider.
- Single wiring choke point: `ApplyProviderToAgent` gained a `cfg`
  parameter and re-applies utility routing on every provider hot-swap
  (TUI, pipe, daemon, desktop, config reload). Vision turn-switches
  re-derive routing idempotently (vendor/endpoint unchanged).
- Health probe (`internal/agent/health_check.go`) deliberately stays on the
  primary provider — it must probe the model that will serve conversation.

## Non-goals

- No cross-vendor utility routing (keep credential surface minimal).
- No automatic "pick the cheapest model" heuristics.
- No changes to compaction logic itself — only which provider executes it.

## Tests

- `internal/agentruntime/utility_route_test.go`: resolution precedence,
  skip cases (unset / same-as-primary / unknown vendor error), install,
  failure-reset, empty-reset, `ApplyProviderToAgent` re-application.
- `internal/agent/utility_provider_test.go`: routing contract
  (primary → utility → reset).
