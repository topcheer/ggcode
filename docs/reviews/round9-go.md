# Round 9 — Go core + Go infra

**Scope**: `internal/agent`, `internal/provider`, `internal/tool`, `internal/session`, `internal/metrics`, `internal/context`, `internal/permission`, `internal/subagent`, `internal/swarm`, `internal/knight`, `internal/config`, `internal/auth`, `internal/mcp`, `internal/a2a`, `internal/harness`, `internal/cron`, `internal/tunnel`, `internal/im`, `internal/webui`, `internal/memory`, `internal/lsp`.

**Date**: 2026-05-29. Round 8 reference: `docs/reviews/round8-go-core.md`, `docs/reviews/round8-go-infra.md`.

---

## Round 8 findings — verified status

### Core (round8-go-core)

| ID | Title | Status | Evidence | Fix |
|----|-------|--------|----------|-----|
| H-01 | Metrics collector goroutine leak | **OPEN** | `internal/metrics/collector.go:20-59,88-96` — `NewCollector()` starts background goroutine; exits only on explicit `Stop()` | Accept `ctx context.Context`; exit on `ctx.Done()` |
| H-02 | Subagent goroutine outlives cancel | **PARTIAL** | `internal/subagent/manager.go:426-475` — cancels immediately but no join | Add `sync.WaitGroup` or done channel per sub-agent; or document async semantics |
| H-03 | `CancelAll()` returns without waiting | **OPEN** | `internal/subagent/manager.go:426-445` | Track spawned goroutines, wait with timeout |
| H-04 | Swarm teammate cleanup on cancel | **PARTIAL** | `internal/swarm/manager.go:95-112,325-350` — cancels but no completion wait | Add teardown completion signaling; await in `Shutdown()`/`CancelAll()` |
| M-01–M-11 | Various medium core items (timeout per LLM call, ctx propagation through tool exec, session JSONL atomicity, `TokenUsage.Add` thread safety, retry-after header, tokenization, Knight circuit breaker, sub-agent path sanitization, swarm task board cap, permission transition validation, context window tool result tokens) | OPEN (sample-verified) | See round8-go-core.md | Per-item fixes carry over |

### Infra (round8-go-infra)

| ID | Title | Status | Evidence | Fix |
|----|-------|--------|----------|-----|
| C-1 (infra) | Config API key exposure via WebUI | **RESOLVED** | `internal/webui/server.go:276-360` routes through `sanitizeConfigForAPI()`; `internal/webui/server_handlers.go:125,185,670-671` returns booleans only | None |
| C-2 (infra) | Tunnel session token reused as encryption key | **OPEN** | `internal/tunnel/crypto.go:13-25` | Split auth token from encryption key; HKDF derive |
| H-05 | JWKS cache no SWR fallback | **OPEN** | `internal/auth/a2a_oauth.go:141-213` — refresh hard-fails on fetch/parse errors | Retain last-good JWKS set; continue validating until newer refresh succeeds |
| H-06 | MCP stdout reader may block | **PARTIAL** | `internal/mcp/client.go:239-273` — now kills/waits with 3s timeout, but `sendRequest()` still relies on stdio reader path; no guaranteed reader shutdown on all failure modes | Close/abort reader transport before `Wait()`; propagate ctx into the read loop |
| H-07 | A2A task transitions lack per-task guard | **OPEN** | `internal/a2a/*` | Serialize transitions per task ID (per-task `sync.Mutex` keyed by task ID) |

---

## New findings (Round 9)

### H — Endpoint metrics/usage maps are data-racy

- **Severity**: High
- **Files**: `internal/session/endpoint_stats.go:23-80`
- **Description**: `Session.EndpointUsage` and `Session.EndpointMetrics` (maps + slices) are mutated with no lock. Three call sites — `AddUsageForEndpoint()`, `AppendMetricForEndpoint()`, `RebuildEndpointStats()` — can be invoked concurrently from agent goroutine + metrics goroutine + UI refresh. Go maps are not safe for concurrent write, so this can `panic: concurrent map writes` under load, plus produce torn slice reads.
- **Fix**: add `sync.Mutex` to `Session` (or a dedicated `endpointStatsMu`); guard all reads/writes; document caller contract. Consider snapshotting (copy-on-read) for UI consumers to avoid holding the lock during render.

### M — Model discovery cache can corrupt under concurrent processes

- **Severity**: Medium
- **Files**: `internal/provider/model_discovery.go:263-340`
- **Description**: Cache uses a single JSON file via read-modify-write with `os.Rename`, but **no inter-process lock**. Two ggcode CLI instances on the same machine can clobber each other's entries. Cache is also unbounded — entries are only evicted via TTL on read, so a long-running daemon discovering many endpoints grows the file monotonically.
- **Fix**:
  - Use `flock(2)` (or `github.com/gofrs/flock`) around read+write.
  - Or shard: one file per provider key.
  - Add a size/entry cap + prune-on-write policy (e.g., keep newest N entries per provider).

### M — Tunnel session hydration/replay can drop queued events on active-session switch

- **Severity**: Medium
- **Files**: `internal/tunnel/broker.go:528-547, 578-590`
- **Description**: Snapshot reseed and replay run in goroutines without a cancel token tied to the session lifetime. A rapid active-session switch (e.g., user closes one session and opens another within a few seconds) lets an older hydrate goroutine **overwrite or interleave** with newer session state.
- **Fix**: add a session-generation counter (or `context.Context` per session) to broker; ignore stale hydration goroutines (`if gen != current { return }`). Reuse the same generation idea on the mobile side for cross-system consistency.

### M — `TokenUsage` remains unsafely mutable across goroutines

- **Severity**: Medium
- **Files**: `internal/provider/provider.go:75-90`; callers in `internal/agent`, `internal/subagent`, `internal/swarm`, `internal/tool/spawn_agent.go`, `internal/tool/skill.go`
- **Description**: `(*TokenUsage).Add()` increments fields directly. Since v1.3.41 the new per-turn/per-endpoint aggregation paths share `TokenUsage` instances across goroutines (sub-agents, swarm teammates, skill forks each can call `Add()` on the parent's `TokenUsage`).
- **Fix**: either make `TokenUsage` immutable (`Add` returns a new value) or guard with a `sync.Mutex` / use `atomic.Int64` fields. Prefer immutable + reduce: each goroutine reports a value, the aggregator reduces.

---

## Cross-cutting themes (Go side)

1. **Goroutine lifecycle still ad-hoc**: subagent/swarm/metrics — all spin goroutines without a guaranteed termination signal. A small library helper like `safego.GoTracked(ctx, name, fn)` returning a `wait()` would unify this.
2. **Persistence atomicity**: session JSONL, model discovery cache, endpoint stats — all read-modify-write without atomic semantics. Round 8 M-03 + Round 9 endpoint maps + Round 9 cache corruption all stem from the same anti-pattern. Standardize on `WriteFile(temp)+Rename()` + flock for shared files; one writer goroutine per session for JSONL.
3. **Race-prone aggregation**: the new metrics/usage feature added significant concurrent surface (per-endpoint maps, `TokenUsage` propagation, hydrate goroutines) without a corresponding locking convention. Suggest a brief design note on which structs are "shared mutable / locked", "shared mutable / atomic", "owned by one goroutine".

---

## Recommended action items

| Priority | Item |
|----------|------|
| P0 | Round-8 H-01 metrics goroutine ctx; Round-9 endpoint stats race; `TokenUsage` thread safety |
| P0 | Session JSONL atomic append (Round-8 M-03) |
| P1 | Subagent + swarm goroutine join-with-timeout |
| P1 | Model discovery cache flock + cap |
| P1 | JWKS SWR fallback |
| P1 | MCP reader transport shutdown ordering |
| P2 | A2A per-task transition guards |
| P2 | Tunnel hydration generation counter |
| P2 | Tunnel auth token != encryption key (HKDF) |
