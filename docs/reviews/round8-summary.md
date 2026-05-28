# Round 8 Full Review — 2025-05-28

## Overview

6 sub-agents reviewed the entire codebase in parallel.

| Reviewer | Scope | Files | Critical | High | Medium | Low |
|----------|-------|-------|----------|------|--------|-----|
| **go-core** | agent, provider, tool, session, metrics, context, permission, subagent, swarm, knight | ~667 LOC | 0 | 4 | 11 | 10 |
| **go-infra** | config, auth, mcp, a2a, harness, cron, tunnel, im, webui, daemon, commands, memory, lsp | ~13 packages | 2 | 3 | 11 | 8 |
| **tui** | internal/tui/ (47+ files), internal/commands/ | ~17.6k LOC | 0 | 3 | 7 | 7 |
| **flutter** | mobile/flutter/ | ~15k LOC | 1 | 4 | 5 | 4 |
| **desktop** | desktop/ggcode-desktop/, ggcode-relay/, desktop/markdownx/ | ~11.5k LOC | 2 | 3 | 7 | 8 |
| **security** | Full codebase (global security audit) | 488 files | 2 | 4 | 6 | 5 |
| **Total** | | | **7** | **21** | **47** | **42** |

**Total findings: 117** (7 Critical / 21 High / 47 Medium / 42 Low)

---

## Critical Findings (7)

### Security / Relay

1. **Relay Zero Authentication** — `ggcode-relay/relay.go:611-617`: WebSocket upgrader with `CheckOrigin: return true` and no token verification. Any client can join any room.
   - **Fix**: Add room-level token authentication via query parameter or first-message handshake.

2. **WebUI WebSocket CheckOrigin Bypass** — `internal/webui/server_websocket.go:16-17`: `CheckOrigin: return true` allows cross-site WebSocket hijacking.
   - **Fix**: Validate Origin header against allowed origins (localhost, 127.0.0.1).

3. **`/nuke` Endpoint Unauthenticated** — `ggcode-relay/relay.go`: Room destruction endpoint has no authentication.
   - **Fix**: Require admin token or remove endpoint from production.

### Go Infra

4. **Config API Key Exposure** — Env var expansion in config may expose expanded API keys through WebUI REST API endpoints.
   - **Fix**: Ensure `SanitizeConfig()` is applied to all config serialization paths.

5. **Tunnel Session Token Exposure** — Tunnel session token serves as both auth credential and encryption key, exposed in connect URL and QR code.
   - **Fix**: Separate auth token from encryption key; use short-lived auth tokens.

### Flutter

6. **Stream Subscription Leak** — `ConnectionNotifier._connectImpl()`: 4 `.listen()` calls never stored/cancelled, causing duplicate callbacks on each reconnect.
   - **Fix**: Store all `StreamSubscription` references and cancel in `dispose()`.

### Desktop

7. **Relay Predictable Temp Files** — Temp files in `/tmp` with predictable names, vulnerable to TOCTOU race.
   - **Fix**: Use `os.CreateTemp()` with random suffix.

---

## High Findings (21)

### Go Core (4)

- **H-01**: `metrics/collector.go` — Goroutine leak if `Stop()` is never called (no context fallback).
- **H-02**: `subagent/manager.go` — Cancelled goroutines briefly continue running (buffer-1 channel mitigation).
- **H-03**: `subagent/manager.go` — `CancelAll()` returns without waiting for goroutine termination.
- **H-04**: `swarm/team.go, idle_runner.go` — Teammate agent resources not cleaned up on cancel.

### Go Infra (3)

- **H-05**: JWKS cache has no stale-while-revalidate fallback — endpoint failure locks out all A2A clients.
- **H-06**: MCP process stdout reader may block indefinitely when subprocess crashes.
- **H-07**: A2A task state machine lacks per-task concurrency guards — concurrent transitions can race.

### TUI (3)

- **H-08**: i18n catalog drift — 13 slash command keys missing in zh-CN (user-facing broken text).
- **H-09**: Duplicated done-handler logic between `doneMsg` and `agentDoneMsg` — divergence risk.
- **H-10**: Inconsistent `program.Send` nil-checking — potential nil pointer on late goroutine callbacks.

### Flutter (4)

- **H-11**: Missing `ref.onDispose()` on `ConnectionNotifier` — provider disposal doesn't cancel subscriptions.
- **H-12**: Two `Future.delayed` closures capturing stale `ref` after provider disposal.
- **H-13**: Unbounded chat message list growth with O(n) copies per append.
- **H-14**: No autoDispose on UI-scoped providers — memory retention.

### Desktop (3)

- **H-15**: Predictable temp file names in `/tmp` — TOCTOU race condition.
- **H-16**: Relay token is predictable workspace ID — no cryptographic verification.
- **H-17**: No connection rate limiting on relay WebSocket.

### Security (4)

- **H-18**: WebUI auth token comparison uses `==` instead of `subtle.ConstantTimeCompare`.
- **H-19**: DingTalk access token potentially leaked in debug logs.
- **H-20**: Config file permissions 0644 — should be 0600.
- **H-21**: Session temp files with world-readable permissions.

---

## Medium Findings (47)

### Go Core (11)
- M-01: Agent stream loop has no timeout per LLM call — hung provider blocks forever.
- M-02: `executeToolWithPermission` doesn't propagate context deadline to tool execution.
- M-03: Session JSONL append is not atomic — crash mid-write corrupts session.
- M-04: `TokenUsage.Add()` is not thread-safe (no mutex on struct fields).
- M-05: Provider retry logic retries on 429 but doesn't respect Retry-After header.
- M-06: `CountTokens` estimation diverges from actual provider tokenization.
- M-07: Knight agent has no circuit breaker for repeated failures.
- M-08: Sub-agent system prompt includes working dir but doesn't sanitize path.
- M-09: Swarm task board has no max task limit — unbounded growth.
- M-10: Permission mode cycle doesn't validate transitions (e.g., bypass → supervised).
- M-11: Context window manager doesn't account for tool result tokens.

### Go Infra (11)
- M-12: Config YAML parsing doesn't validate unknown keys — silent typo failures.
- M-13: A2A OAuth2 token refresh has no retry on transient network errors.
- M-14: MCP server process cleanup on SIGTERM is not guaranteed.
- M-15: Tunnel event replay doesn't verify event ordering.
- M-16: IM adapter pairing has no expiry — stale pairings persist forever.
- M-17: WebUI session API returns full session history — potential info leak.
- M-18: Cron job execution has no distributed locking — double-fire risk.
- M-19: Harness worktree cleanup is not atomic — partial cleanup leaves artifacts.
- M-20: Memory project files loaded from untrusted paths — path traversal.
- M-21: LSP client has no timeout for definition/references requests.
- M-22: Daemon bridge doesn't validate IM message content length.

### TUI (7)
- M-23: Missing IM emit in `handleAgentDoneMsg` — IM channels don't get final message.
- M-24: Wechat/wecom panel dual-close — panel state inconsistency.
- M-25: No panel resize propagation on terminal resize.
- M-26: Approval channel blocking — tool approval blocks entire event loop.
- M-27: Spinner overhead on rapid updates — unnecessary redraws.
- M-28: Aggressive approval prefix match — false positives on similar tool names.
- M-29: Autocomplete triggers on every keystroke — no debounce.

### Flutter (5)
- M-30: Broad SDK constraint (`>=3.0.0 <4.0.0`) — allows untested Flutter versions.
- M-31: Stale `MainActivity.kt` at old package path.
- M-32: Android release build without minification — larger APK.
- M-33: Flush timer reentry issue — concurrent flush operations.
- M-34: Silent error swallowing in catch blocks — no logging.

### Desktop (7)
- M-35: Pending message recursive goroutine chain — stack overflow risk.
- M-36: Unbounded HTTP response read from kroki/mermaid — memory exhaustion.
- M-37: `destroyRoom` fire-and-forget — no cleanup confirmation.
- M-38: `offlineTimer` race condition — concurrent timer operations.
- M-39: `thinkingW` widget race — concurrent UI updates.
- M-40: IM HTTP client has no timeout — hung requests block indefinitely.
- M-41: WebSocket double-close — panic on already-closed connection.

### Security (6)
- M-42: Relay has no TLS in production — plaintext WebSocket.
- M-43: Tunnel token exposed in URL query parameters — server logs.
- M-44: MCP `client_id` logged at INFO level — credential leak.
- M-45: WebUI token-in-URL mode — bookmarkable URLs with auth.
- M-46: A2A JWKS polling has no backoff — DoS on endpoint failure.
- M-47: OAuth2 state parameter has no expiry — replay attacks.

---

## Low Findings (42)

Summarized by category:

- **Code quality**: 15 findings (unused imports, inconsistent error messages, missing godoc)
- **Testing**: 8 findings (flaky tests, missing edge cases, test isolation)
- **Performance**: 7 findings (unnecessary allocations, missing caches, O(n) scans)
- **Documentation**: 6 findings (missing README sections, outdated comments)
- **Build/CI**: 6 findings (Dockerfile version drift, missing CI workflows)

---

## Cross-Cutting Themes

### 1. Relay Authentication Gap (Critical, recurring since Round 5)
The relay server has been flagged as zero-auth in every review round since Round 5. This remains the #1 security risk. Any client with the workspace ID can join any room and read/write tunnel events.

### 2. WebSocket CheckOrigin (Critical, recurring since Round 5)
Both relay and webui use `CheckOrigin: return true`. This has been flagged in Rounds 5, 6, 7, and now 8. The fix is straightforward but has not been applied.

### 3. Goroutine Lifecycle Management (High)
Multiple findings across subagent manager, swarm, and metrics collector relate to goroutine cleanup on cancellation. The pattern of "fire goroutine without ensuring termination" appears in at least 4 locations.

### 4. Flutter Stream Leaks (Critical → High, improving)
The stream subscription leak has been flagged since Round 5. The current finding is more specific (4 unnamed `.listen()` calls in `_connectImpl`), suggesting partial fixes were applied but the core issue remains.

### 5. i18n Drift (High, new)
The TUI's i18n system has 13 missing zh-CN keys for newer slash commands. This is a new finding, likely introduced by recent feature additions without corresponding i18n updates.

---

## Recommendations

### Immediate (before next release)
1. Fix relay authentication — add room-level token verification.
2. Fix WebUI WebSocket CheckOrigin — validate against localhost origins.
3. Fix WebUI token comparison — use `subtle.ConstantTimeCompare`.
4. Fix Flutter stream subscription leak — store and cancel all subscriptions.

### Short-term (next sprint)
5. Add goroutine lifecycle tracking to subagent manager and swarm.
6. Fix i18n catalog drift — add missing zh-CN keys.
7. Deduplicate TUI done-handler logic.
8. Add config file permission enforcement (0600).

### Medium-term (next quarter)
9. Implement relay TLS termination.
10. Add connection rate limiting to relay and webui.
11. Add session JSONL atomic writes.
12. Implement stale-while-revalidate for JWKS cache.

---

*Report generated by Round 8 review team (6 parallel reviewers)*
*Individual reports: docs/reviews/round8-go-core.md, round8-go-infra.md, round8-tui.md, round8-flutter.md, round8-desktop.md, round8-security.md*
