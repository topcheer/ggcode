# Round 9 Full Review — 2026-05-29

## Overview

Five parallel reviewers audited the full ggcode stack (Go core/infra, TUI, desktop, mobile+relay, security/release) **one day after Round 8** (2025-05-28) to verify the state of Round-8 findings after the v1.3.40 → v1.3.48 release wave (8 releases in 3 days) and to audit the newly added features:

- Dynamic terminal window title (OSC) — v1.3.48
- Paste shortcut hints across all TUI panels — v1.3.47
- Native macOS titlebar integration — v1.3.47
- `save_memory` live prompt refresh — v1.3.47
- Turn metrics digest in chat, sidebar hidden by default — v1.3.46
- Per-endpoint usage/metrics aggregation, model discovery cache, MiMo vendor headers — v1.3.45
- TUI stats panel, desktop metrics window — v1.3.44
- Per-turn usage history, async LLM/tool performance metrics — v1.3.43
- XiaoMi MIMO provider with 8 models — v1.3.42
- TokenUsage propagation to sub-agents/swarm/skill + cache hit % — v1.3.41
- Desktop MSI + Linux packages (deb/rpm/apk/ipk/pkg.tar.zst) — v1.3.40
- Relay 12h expiry, hydrate session, broker active_session on reconnect — v1.3.37-v1.3.39
- Tunnel preserve tool metadata, fix ask_user delivery, cron user_message — v1.3.38

| Reviewer | Scope | Round 8 verified | New findings (C/H/M/L) |
|----------|-------|------------------|------------------------|
| **go-core+infra** | agent, provider, tool, session, metrics, context, subagent, swarm, knight, config, auth, mcp, a2a, harness, cron, tunnel, im, webui, memory, lsp | 9 | 0 / 1 / 3 / 0 |
| **tui** | `internal/tui/` 47+ files, `internal/commands/`, `internal/chat/` | 10 | 0 / 0 / 2 / 4 |
| **desktop** | `desktop/ggcode-desktop/`, `desktop/markdownx/` | 5 | 0 / 0 / 9 / 4 |
| **mobile+relay** | `mobile/flutter/lib/`, `ggcode-relay/` | 13 | 0 / 3 / 4 / 2 |
| **security+release** | Full codebase + `.goreleaser.yaml`, `.github/workflows/`, install wrappers, packaging | 9 + 16 audits | 0 / 0 / 6 / 0 |

**Status of Round 8 findings (verified)**: 5 RESOLVED, 7 PARTIAL, 22 OPEN, 1 DESIGN-INTENDED.

**Round 9 new findings**: 0 Critical / 4 High / 24 Medium / 10 Low.

---

## Round 8 Findings — Verified Status

### Critical

| # | Title | Status | Evidence |
|---|-------|--------|----------|
| C-1 | Relay zero auth | **OPEN** | `ggcode-relay/relay.go:609-630, 724-760` — still only checks token-presence as room ID |
| C-2 | WebUI WebSocket CheckOrigin bypass | **DESIGN-INTENDED** | `internal/webui/server_websocket.go:16-18` — confirmed by `docs/design-decisions.md:60-91` (127.0.0.1 + token auth) |
| C-3 | `/nuke` unauthenticated | **OPEN** | `ggcode-relay/relay.go:738-760` |
| C-4 | Config API key exposure | **RESOLVED** | `internal/webui/server.go:276-360` routes through `sanitizeConfigForAPI()`; `server_handlers.go:125,185,670-671` returns booleans only |
| C-5 | Tunnel token = encryption key | **OPEN** | `internal/tunnel/crypto.go:13-25` |
| C-6 | Flutter stream subscription leak | **OPEN** | `mobile/flutter/lib/core/providers/connection_provider.dart:165-209` — still 4 unstored `.listen()` calls |
| C-7 | Desktop predictable temp files | **OPEN** | `desktop/ggcode-desktop/chat_view.go:228-293`, `main.go:34-35` — fixed names `ggcode-clipboard-paste.png`, `ggcode-icon.png` |

**Action**: C-1, C-3, C-5, C-6, C-7 are the **5 unresolved criticals carrying over**. C-1/C-3/H-16/H-17/M-42 form the relay-security cluster which has been the #1 recurring finding since Round 5.

### High (selected)

| # | Title | Status | Evidence |
|---|-------|--------|----------|
| H-01 | Metrics collector goroutine leak | OPEN | `internal/metrics/collector.go:20-59,88-96` |
| H-02 | Subagent goroutine outlives cancel | PARTIAL | `internal/subagent/manager.go:426-475` |
| H-03 | `CancelAll()` no wait | OPEN | `internal/subagent/manager.go:426-445` |
| H-04 | Swarm teammate cleanup on cancel | PARTIAL | `internal/swarm/manager.go:95-112,325-350` |
| H-05 | JWKS no SWR fallback | OPEN | `internal/auth/a2a_oauth.go:141-213` |
| H-06 | MCP stdout reader may block | PARTIAL | `internal/mcp/client.go:239-273` — 3s kill timeout added, reader path still fragile |
| H-07 | A2A task transitions lack per-task guard | OPEN | `internal/a2a/*` |
| H-08 | i18n drift (zh-CN keys missing) | PARTIAL | `internal/tui/i18n_zh.go` — no completeness check |
| H-09 | done-handler duplication | OPEN | `internal/tui/update_done.go:12-43,48-82` |
| H-10 | Inconsistent `program.Send` nil checks | PARTIAL | `internal/tui/submit.go:101-109,154-160,259,306,333,370,381,436,446,450,452` |
| H-11 | Mobile missing `ref.onDispose` | OPEN | `mobile/flutter/lib/core/providers/connection_provider.dart:40-58,231-250` |
| H-12 | Mobile stale `Future.delayed` | OPEN | `mobile/flutter/lib/core/providers/chat_provider.dart:161-197` |
| H-13 | Mobile unbounded chat list O(n) | OPEN | `mobile/flutter/lib/core/providers/chat_provider.dart:152-259` |
| H-14 | No autoDispose on UI providers | OPEN | `mobile/flutter/lib/core/providers/connection_provider.dart:19-22` |
| H-16 | Relay predictable workspace ID token | OPEN | `ggcode-relay/relay.go:617-630` |
| H-17 | No relay rate limiting | OPEN | All relay HTTP/WS handlers |
| H-18 | WebUI auth token `==` comparison | OPEN | `internal/webui/auth.go:32-43` |
| H-19 | DingTalk token in debug logs | OPEN | `internal/im/dingtalk_adapter.go` |
| H-20 | Config file perms 0644 | OPEN | `internal/config/config_save.go:59-63,138-142` |
| H-21 | Session temp files world-readable | RESOLVED | model_discovery now writes 0600 in 0700 dir |

### Medium (selected resolved/partial)

| # | Title | Status |
|---|-------|--------|
| M-25 | TUI panel resize | **RESOLVED** (`internal/tui/resize.go:19-35`) |
| M-33 | Mobile flush timer reentry | **RESOLVED** (`workspace_cache.dart:1056-1062`) |
| M-40 | Desktop IM HTTP client no timeout | **RESOLVED** (`im_bridge.go:170-176,248-271`) |
| M-23 | Missing IM emit on `handleAgentDoneMsg` | OPEN |
| M-24 | wechat/wecom dual-close | OPEN |
| M-27 | Spinner overhead | OPEN |
| M-28 | Aggressive approval prefix match (`y*`/`n*` ≤3 chars) | OPEN |
| M-29 | Autocomplete debounce | OPEN |
| M-35 | Desktop recursive goroutine chain | OPEN |
| M-36 | Unbounded kroki/mermaid HTTP read | OPEN |
| M-39 | Desktop thinkingW widget race | OPEN |

---

## Round 9 New Findings

### High (3)

1. **Endpoint metrics/usage maps are data-racy** (`internal/session/endpoint_stats.go:23-80`).
   - `Session.EndpointUsage` / `EndpointMetrics` maps + slices mutated with no lock; any concurrent `AddUsageForEndpoint()` / `AppendMetricForEndpoint()` / `RebuildEndpointStats()` call can race or panic.
   - **Fix**: add `sync.Mutex` to `Session`; guard all reads/writes; document caller contract.

2. **Relay 12h expiry boundary hard-closes — no graceful migration** (`ggcode-relay/store.go:358-425`, `ggcode-relay/relay.go:501-547`).
   - DB retention is 12h but live-room expiry still driven by 5m offline timer. At expiry the room is deleted and clients receive `sharing_stopped` with no migration path.
   - **Fix**: add explicit `room_expired` protocol message + mobile-side permanent-failure handling; consider extending live retention while connected.

3. **Relay session hydrate swap is non-atomic** (`ggcode-relay/relay.go:255-341`).
   - `sessionID`, history reset, DB hydrate, client broadcast happen under one lock, but hydrated history is loaded from DB while the room is already mutated; encrypted events arriving mid-swap can interleave or leak across sessions.
   - **Fix**: stage hydrate into a temp snapshot, then swap atomically; tag every event with a session generation.

4. **Replay ordering across session swap is brittle** (`ggcode-relay/relay.go:322-397`, `mobile/flutter/lib/core/providers/connection_provider.dart:277-453`).
   - Mobile applies `active_session` before local snapshot restore and relies on ordinal dedup. Order is *mostly* preserved but mid-swap arrivals can still interleave.
   - **Fix**: strict session generation token; sequence replay against it; add tests for mid-session QR rescans.

### Medium (24)

Group A — concurrency / lifecycle:

- **TokenUsage remains unsafely mutable** (`internal/provider/provider.go:75-90`) — `(*TokenUsage).Add()` field-increments called from multiple goroutines after the per-turn/per-endpoint aggregation work. Fix: immutable type or mutex.
- **Model discovery cache can corrupt on concurrent process use** (`internal/provider/model_discovery.go:263-340`) — read-modify-write + `os.Rename` with no inter-process lock. Fix: flock or per-key files. (Cache size unbounded; add cap & prune on write.)
- **Tunnel hydration/replay can drop events on active-session switch** (`internal/tunnel/broker.go:528-547,578-590`) — no cancel token on hydrate goroutines. Fix: generation counter + cancelable replay context.
- **Reconnect refactor misses concurrent reconnect suppression** (`connection_provider.dart:93-109,222-229`) — `_connectInFlight` dedupes same-URL only; disconnect/reconnect can race with in-flight subs. Fix: connection generation; ignore stale completions.
- **App background >90s recovery is incomplete** (`mobile/flutter/lib/main.dart:116-155`, `connection_service.dart:180-239`) — only reconnects if previously connected; doesn't reconcile relay's server-offline hints or long-bg cleanup. Fix: on resume, always revalidate room/session; clear stale resume on permanent failure.
- **Token expiry mid-stream handled only partially** (`connection_service.dart:32-38,316-321`) — HTTP 410 maps to permanent, mid-stream expiry leaves stale state until next reconnect. Fix: clear resume state immediately on permanent failure.

Group B — desktop:

- **macOS titlebar ignores system appearance** (`desktop/ggcode-desktop/titlebar_darwin.go:14-25,90-105`) — forces `NSAppearanceNameDarkAqua`. Fix: derive from current theme/system; reapply on flips.
- **Titlebar CGo helper has unsafe lifetime assumptions** (`titlebar_darwin.go:140-143,189-191`) — `C.free` without `<stdlib.h>` include; closures call Obj-C without explicit main-thread guarantee. Fix: include `<stdlib.h>`; ensure Cocoa calls on main thread.
- **`themedRectangle.Refresh()` is re-entrant and closure-heavy** (`chat_view.go:39-57`) — per-instance closures; refresh may cascade. Fix: pure data recompute.
- **Onboarding endpoint selector uses display name as identifier** (`app.go:882-899,948-960,982-988`) — duplicate display names bind wrong endpoint. Fix: hidden stable IDs.
- **Session token usage persistence non-atomic** (`agent_bridge.go:1068-1098`) — usage + metadata + usage-entry written separately; crash desyncs. Fix: single transaction / serialized writer.
- **Token usage updates can race with session switches** (`agent_bridge.go:1068-1098,1833,2365`) — UI may see stale totals when sessions swap fast. Fix: version usage updates per session; drop stale callbacks.
- **`fyne.CurrentApp()` nil/window-index panic risk** (`chat_view.go:388-401`) — `AllWindows()[0]` assumption. Fix: guard.
- **macOS titlebar not guarded for missing controls** (`titlebar_darwin.go:27-53,55-87`) — `standardWindowButton:` can be nil in fullscreen/custom chrome states. Fix: re-check on transitions; no-op cleanly.
- **Linux packaging likely incomplete for desktop integration** (`.goreleaser.yaml:43-70`, `.github/packaging/linux/*`) — packaging metadata exists but release config packages only CLI binary; no explicit install of `.desktop` / icon / MIME files. Fix: ensure desktop package installs those assets.
- **Windows MSI lacks association/uninstall cleanup** (`.github/packaging/windows/ggcode*.wxs`) — no file-assoc keys nor explicit cleanup beyond Program Menu folder. Fix: add association registry keys + uninstall cleanup.

Group C — TUI:

- **Terminal title not width-truncated** (`internal/tui/model.go:903-989`) — long CJK titles can exceed terminal limits. Fix: display-width truncate before OSC emit.
- **`save_memory` live refresh races active runs** (`internal/tui/model.go:888-895`, `submit.go:67-75`) — `rebuildSystemPrompt()` mutates shared agent state from UI path. Fix: serialize against active runs or queue for the agent loop.

Group D — mobile/replay:

- **Pending queue hidden submissions not clearly excluded from UI projection** (`connection_provider.dart:383-413`, `chat_provider.dart:213-259`) — rebuild operates on full list; cron `user_message` rendering policy unclear here.
- **iOS background→foreground recovery is socket-status-driven, not session-integrity-driven** (`mobile/flutter/lib/main.dart:126-139`).

Group E — security/release:

- **Model discovery cache unbounded** (`internal/provider/model_discovery.go`) — TTL eviction only on read; add size/entry cap + prune on write.
- **Relay 12h expiry state cleanup partial** — ensure expiry deletes rows + cursors + history.
- **Hydrate on sessionID change** — bind history strictly to session/room identity; verify no cross-session replay.
- **CI doesn't call `make verify-ci`** (`.github/workflows/ci.yml`) — calls direct commands; drift risk. Fix: wire to `make verify-ci`.
- **No release-version smoke assertions** (`.github/workflows/release.yml:23-39`) — 8 releases in 3 days, no version-bump gate. Fix: changelog/version consistency checks; per-package download/`--version` smoke.

### Low (10)

- Paste hint line not width-clipped (`paste_placeholder.go`).
- Stats panel rebuilds full string every render (`stats_panel.go:51-113`).
- Titlebar label lookup tag collision risk (`titlebar_darwin.go:11,64-73`).
- Theme switch refresh can flicker (`app.go:1282-1299`).
- Onboarding persistence partial on intermediate selections (`app.go:908-916,1003-1007`).
- Mobile network change (wifi↔cellular) not explicitly handled (`main.dart:116-155`).
- Tap-to-dismiss keyboard placement safe today (verified) but should be re-tested as message list gains long-press menu.
- Battery/wake-lock/ping interval not adaptive (`connection_service.dart:330-335`).
- Android background notifications not implemented (out of scope per current MR).
- `statusActivity` strings vary by path with hardcoded English in: `commands_slash_admin.go:448,473`, `commands.go:541`, `tunnel.go:699`, `commands_harness_task.go:33,81` (impacts new terminal title quality + IM messaging).

---

## Cross-Cutting Themes

### 1. Relay auth gap (Critical, 5th round in a row)
Round 5/6/7/8/9 all flagged this. No partial progress; nothing has been done at the auth layer. The 12h-expiry + hydrate work *increased* risk because session-bound state lives longer with the same security model. **Recommendation**: this is now release-blocking for any production/multi-user deployment.

### 2. Concurrency / lifecycle hygiene (High, persistent)
At least **8 distinct race/leak findings** across goroutine cancellation, map mutation, file cache, hydrate replay, and session token usage. Symptom of treating concurrency as ad-hoc rather than enforced by patterns. **Recommendation**: introduce a small "actor"/"single-writer per resource" convention and apply consistently to `Session`, `TokenUsage`, model discovery cache, tunnel broker hydrate, mobile reconnect.

### 3. Persistence atomicity (Medium, expanding)
Session JSONL append, model discovery cache, desktop token usage, onboarding config — all do read-modify-write without atomic semantics. With v1.3.x metrics now persisting per turn, the surface grew. **Recommendation**: standardize on `os.WriteFile(temp)+os.Rename()` with flock for shared files; serialize all session-bound writes through one writer goroutine.

### 4. i18n + status text drift (High, new since Round 8)
Round 8 flagged 13 missing zh-CN keys. Round 9 confirms it's still open and the new terminal-title feature has surfaced English-only `statusActivity` strings in 6+ locations. As the terminal title and IM emit messages now propagate these strings, they're more user-visible. **Recommendation**: introduce a `statusActivity()` helper that always goes through `m.t(...)` keys + add a unit test asserting zh-CN/en parity.

### 5. Release pace vs. CI rigor (Medium, new theme)
8 releases in 3 days. CI does run gofmt/vet/test but doesn't gate on changelog/version consistency, doesn't run `make verify-ci` (which exists), doesn't do package-level smoke. **Recommendation**: add `release-blocker` workflow that downloads each published artifact and runs `--version`; require changelog entry for any release.

### 6. Mobile resilience improving but still brittle (improving)
Tap-to-dismiss-keyboard, room-not-found-permanent, instance-bound resume — all good. But stream subscription leak, autoDispose, chat-list growth, ref.onDispose persist since Round 5+. **Recommendation**: dedicate one mini-sprint to a connection_provider rewrite that uses `ref.onDispose`, stored subscriptions, generation counters, bounded list.

---

## Recommendations (prioritized action plan)

### Release-blocking (do before next deployment to multi-user)

1. **Relay auth (C-1, C-3, H-16)** — implement signed room tokens (HMAC over `room_id|server_id|exp`). Reject WS upgrades that don't present a valid signed token.
2. **Relay TLS (M-42)** — terminate TLS at relay or enforce wss:// in mobile/desktop brokers.
3. **Relay rate limiting (H-17)** — per-IP+token throttle on WS connect and on event publish.
4. **Predictable temp files (C-7)** — switch to `os.CreateTemp()` for clipboard + icon paths.
5. **Flutter stream leak (C-6, H-11, H-14)** — store and cancel all `.listen()`; add `ref.onDispose`.

### Short-term (next sprint)

6. **Concurrency hardening**: add `sync.Mutex` to `Session.EndpointUsage`/`EndpointMetrics`; make `TokenUsage` immutable; flock model discovery cache; generation-counter the tunnel hydrate and the mobile reconnect.
7. **Goroutine lifecycle**: add `WaitGroup`-based join with timeout to `subagent.CancelAll()` and `swarm.Manager.Shutdown()`; bind `metrics.Collector` to a context.
8. **i18n + statusActivity hardening**: parity test for zh-CN/en; route all `statusActivity` writes through `m.t(...)`.
9. **TUI cleanup**: deduplicate done-handler (H-09); remove `y*`/`n*` prefix approval fallback (M-28); debounce autocomplete (M-29); width-truncate terminal title.
10. **Desktop fixes**: titlebar appearance follows system mode; CGo `<stdlib.h>` include; onboarding endpoint stable ID; persistence atomicity for usage writes.

### Medium-term (next quarter)

11. **Session JSONL atomic writes** with single-writer goroutine per session.
12. **JWKS stale-while-revalidate fallback** (`internal/auth/a2a_oauth.go`).
13. **MCP process cleanup**: close reader transport before `Wait()`; propagate ctx into read loop.
14. **Relay 12h migration protocol**: graceful `room_expiring` warning + migration token for in-flight sessions.
15. **Packaging**: Linux desktop integration assets (.desktop / icon / MIME); Windows MSI association + uninstall cleanup; mobile target-SDK and privacy-manifest validation in CI.
16. **CI**: wire `make verify-ci`; per-package `--version` smoke job; release-version gate.

### Long-term (next half)

17. **Knight learning loop integration** (already designed in `docs/design/knight-auto-evolution.md`) — implement.
18. **Cross-instance shared memory** — see `docs/plans/2026-05-29-round9-creative-features.md`.
19. **Pair-session relay protocol** — multi-developer single-session via the same relay.
20. **Compliance/audit log signing** — for enterprise adoption.

---

## Per-Reviewer Detail

- `docs/reviews/round9-go.md` — go-core + go-infra
- `docs/reviews/round9-tui.md` — TUI
- `docs/reviews/round9-desktop.md` — Desktop (Fyne) + macOS native titlebar
- `docs/reviews/round9-mobile-relay.md` — Flutter + relay
- `docs/reviews/round9-security-release.md` — security, release, packaging, cross-platform
- `docs/plans/2026-05-29-round9-creative-features.md` — creative new feature proposals (18 ideas, prioritized & sketched)

---

*Round 9 review generated 2026-05-29. Reviewers: 5 parallel explore agents + author synthesis.*
