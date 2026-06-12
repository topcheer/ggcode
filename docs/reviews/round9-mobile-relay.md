# Round 9 — Mobile (Flutter) + Relay (Go)

**Scope**: `mobile/flutter/lib/` (Dart) and `ggcode-relay/` (Go).

**Date**: 2026-05-29. Round 8 references: `docs/reviews/round8-flutter.md`, `docs/reviews/tunnel-relay-flutter.md`, `docs/reviews/tunnel-relay-go.md`, `docs/reviews/tunnel-relay-cross-cut.md`.

---

## Round 8 findings — verified status

### Flutter

| ID | Title | Status | Evidence | Fix |
|----|-------|--------|----------|-----|
| C-6 | StreamSubscription leak | **OPEN** | `mobile/flutter/lib/core/providers/connection_provider.dart:165-209` — 4 `.listen()` calls still unstored | Store all subs as fields; cancel on reconnect/dispose |
| H-11 | Missing `ref.onDispose` | **OPEN** | `connection_provider.dart:40-58, 231-250` — cleanup relies on ad hoc disconnect | Add `ref.onDispose(() => service?.dispose())` + cancel listeners |
| H-12 | Stale `Future.delayed` | **OPEN** | `chat_provider.dart:161-197` — `addUserMessage()` schedules uncancelled 5s callback | Use `Timer` + cancel in dispose; or guard via mounted/generation |
| H-13 | Unbounded chat list O(n) | **OPEN** | `chat_provider.dart:152-259` — state grows without cap; every mutation copies full list | Cap/paginate per session (e.g., keep newest 500 in memory, persist rest to sqlite cache) |
| H-14 | No autoDispose on UI providers | **OPEN** | `connection_provider.dart:19-22` | Add `autoDispose` or explicit lifecycle cleanup |
| M-30 | Broad SDK constraint | **OPEN** | `pubspec.yaml:6-8` — still `>=3.0.0 <4.0.0` | Tighten floor to tested Dart (e.g., `>=3.4.0 <4.0.0`) |
| M-31 | Stale `MainActivity.kt` at old package path | **OPEN** | `mobile/flutter/android/app/src/main/kotlin/gg/ai/ggcode/ggcode_mobile/MainActivity.kt` | Delete dead file |
| M-32 | Android release minification disabled | **OPEN** | `build.gradle.kts:48-53` — `isMinifyEnabled = false`, `isShrinkResources = false` | Enable R8 + resource shrinking; add ProGuard rules |
| M-33 | Flush timer reentry | **RESOLVED** | `workspace_cache.dart:1056-1062` — `_scheduleFlush()` cancels before recreating | None |
| M-34 | Silent error swallowing | **OPEN** | `workspace_cache.dart:358-425` — cleanup/DB failures swallowed in some paths | Log rollback/cleanup failures via the existing log pipeline |

### Relay

| ID | Title | Status | Evidence | Fix |
|----|-------|--------|----------|-----|
| #1 | Zero auth | **OPEN** | `ggcode-relay/relay.go:609-630, 724-760` — only checks token-presence | Add signed-token validation (HMAC over room ID + expiry) |
| #3 | `/nuke` unauthenticated | **OPEN** | `ggcode-relay/relay.go:738-746` | Require admin token; remove from production |
| H-16 | Predictable workspace ID token | **OPEN** | `ggcode-relay/relay.go:617-630` — room identity = raw `token` query | Require signed, expiring token |
| H-17 | No rate limiting | **OPEN** | All handlers | Per-IP + per-token throttle on WS upgrade + event publish |
| M-42 | No TLS | **OPEN** | `ggcode-relay/relay.go:611-630` + mobile supports `ws://` | Serve behind TLS; enforce `wss://` in clients |

---

## New findings (Round 9)

### H — Relay 12h expiry boundary hard-closes, no graceful migration

- **Severity**: High
- **Files**: `ggcode-relay/store.go:358-425`, `ggcode-relay/relay.go:501-547`
- **Description**: DB retention is 12h, but live-room expiry still driven by a 5m offline timer. When expiry hits, the room is deleted and clients receive `sharing_stopped` with no migration path. A long-running mobile session that backgrounds for several hours but stays connected via reconnects can hit the 12h boundary unexpectedly.
- **Fix**:
  - Define an explicit `room_expiring(t)` warning event ~10 minutes before expiry.
  - Add a `room_expired` terminal event that the mobile treats as permanent failure.
  - Optionally: extend the live retention while a client is actively connected (sliding window).

### H — Session hydrate swap is non-atomic

- **Severity**: High
- **Files**: `ggcode-relay/relay.go:255-341`
- **Description**: `sessionID` update, history reset, DB hydrate, and client broadcast happen under one lock — but hydrated history is loaded from DB **while the room is already mutated**. Cross-session encrypted events arriving during the swap can be applied to the wrong session generation or leak across sessions.
- **Fix**: stage hydrate into a temp snapshot, then swap atomically:
  ```go
  snap := loadHistory(newSessionID)  // off-lock
  lock()
  defer unlock()
  if room.sessionID != prevSessionID { return /* concurrent swap; retry */ }
  room.sessionID = newSessionID
  room.history = snap
  room.generation++
  broadcast(activeSessionMsg{Gen: room.generation, SessionID: newSessionID})
  ```

### H — Replay ordering across session swap is brittle

- **Severity**: High
- **Files**: `ggcode-relay/relay.go:322-397`, `mobile/flutter/lib/core/providers/connection_provider.dart:277-453`
- **Description**: The relay sends `active_session` → `resume_ack` → replay; mobile applies `active_session` before local snapshot restore and relies on ordinal dedup. Order is mostly preserved but mid-swap event arrivals can still interleave with cache restore. Per the prior user emphasis ("无论如何扫码之前的所有这个session的消息是需要推送到移动端的"), this is a correctness-critical path.
- **Fix**: introduce a session-generation token in every event. Mobile drops anything with an older generation than the latest `active_session`. Add an integration test simulating mid-swap event arrival.

### M — Reconnect refactor misses concurrent reconnect suppression

- **Severity**: Medium
- **Files**: `connection_provider.dart:93-109, 222-229`
- **Description**: `_connectInFlight` dedupes same-URL connects, but `disconnect()/reconnect()` can still race with an in-flight connection and old subscriptions.
- **Fix**: add a per-`ConnectionNotifier` generation counter; every async completion checks `if (gen != _currentGen) return;`.

### M — App background >90s recovery is incomplete

- **Severity**: Medium
- **Files**: `mobile/flutter/lib/main.dart:116-155`, `mobile/flutter/lib/core/connection_service.dart:180-239`
- **Description**: App resumes reconnecting only if previously connected, but doesn't reconcile against relay's server-offline hints or long-bg expiry cleanly. The 90s offline grace was added but the resume path doesn't always revalidate.
- **Fix**: on `AppLifecycleState.resumed`, always call a `revalidateSessionState()` that probes relay; clear stale resume on permanent failure (HTTP 410 / room-not-found).

### M — Token expiry mid-stream handled only partially

- **Severity**: Medium
- **Files**: `connection_service.dart:32-38, 316-321`
- **Description**: HTTP 410 / room-not-found map to permanent failure, but mid-stream token expiry (signed-token introduction needed; see relay-side fixes) can leave stale state until the next reconnect path fires.
- **Fix**: on any permanent failure (auth + room), immediately clear resume state and notify UI; do not wait for next ws frame.

### M — Pending queue hidden submissions not clearly excluded from UI projection

- **Severity**: Medium
- **Files**: `connection_provider.dart:383-413`, `chat_provider.dart:213-259`
- **Description**: Cron events were changed to `user_message` (v1.3.38). The mobile rebuild logic operates on the full list; there's no explicit hidden-submission projection path here. If the relay forwards a hidden cron prompt as `user_message`, it can render on mobile as a regular user-sent message.
- **Fix**:
  - Add a `hidden: true` flag (or `origin: "cron"|"subagent"|"user"`) to the message protocol.
  - Mobile filters `hidden: true` from the rendered list but keeps them in the persisted cache for resume integrity.

### L — Network change (wifi↔cellular) not explicitly handled

- **Severity**: Low
- **Files**: `mobile/flutter/lib/main.dart:116-155`
- **Description**: No connectivity-state listener; recovery depends on socket failure timing.
- **Fix**: integrate `connectivity_plus` (already commonly used in Flutter); on transition, force a reconnect probe.

### L — Battery/wake-lock/ping interval not adaptive

- **Severity**: Low
- **Files**: `mobile/flutter/lib/core/connection_service.dart:330-335`, `mobile/flutter/lib/main.dart:163-169`
- **Description**: Ping is every 20s, wakelock enabled whenever connected. Drains battery during long idle.
- **Fix**: adaptive ping (60s when idle, 20s during active stream); release wakelock when no activity > N minutes.

---

## Cross-cutting (mobile ↔ relay)

1. **Generation token end-to-end** — most of the H findings above resolve cleanly if every relay event carries a monotonic session generation. Mobile drops stale; relay refuses out-of-order writes. This single change is the highest-leverage fix on this surface.
2. **Auth is still missing** (Round 5 → Round 9). Until a signed-token + TLS pair is shipped, the entire relay path is treated as untrusted by careful users; this caps mobile adoption.
3. **Persistence ↔ projection gap** — the new `hidden`/`origin` field is necessary to keep cron + subagent + hidden prompts out of mobile chat while preserving them in resume. Currently the mobile relies on inferring "hidden" from missing-text-in-chat heuristics.

---

## Recommended action items

| Priority | Item |
|----------|------|
| P0 | Relay signed-token auth (#1, H-16) |
| P0 | Relay TLS / enforce `wss://` (M-42) |
| P0 | Per-IP rate limit on WS + event publish (H-17) |
| P0 | Remove or hard-protect `/nuke` (#3) |
| P0 | Flutter `connection_provider` rewrite: store + cancel subs, autoDispose, ref.onDispose, generation counter (C-6, H-11, H-14, new M) |
| P0 | Session hydrate atomicity + generation token end-to-end (new H × 3) |
| P1 | Relay 12h graceful expiry protocol (new H) |
| P1 | Pending-queue `hidden`/`origin` field across protocol + mobile filtering (new M) |
| P1 | Mobile chat list cap + persist tail (H-13) |
| P1 | Mobile `addUserMessage` Future.delayed cancellation (H-12) |
| P2 | Android R8 + resource shrinking (M-32) |
| P2 | iOS/Android target-SDK + privacy-manifest validation in CI |
| P2 | Connectivity-state-aware reconnect (new L) |
| P2 | Adaptive ping + wakelock release (new L) |
| P3 | Delete stale `MainActivity.kt` (M-31); tighten SDK floor (M-30); log cleanup failures (M-34) |

---

## 2026-05-29 addendum — share v2 rollout status

### Current progress

- Relay-issued share tickets are now implemented and deployed: relay owns `GGCODE_SHARE_V2_SECRET`, exposes `POST /share/session`, and returns server/client auth tickets plus the host renew token.
- Host-side v2 no longer depends on shipping the signing secret inside distributed TUI/desktop/daemon binaries; the host only generates the `crypto_key` locally.
- Mobile already understands both legacy and v2 share descriptors and keeps renew-token state local instead of embedding it into the public share URL.
- Production Railway relay has the v2 signing secret configured, and the deployment carrying service-issued tickets has already been rolled out.
- Manual production verification passed for the full v2 happy path against `https://gateway.ggcode.dev`: issue session -> server auth-ticket connect -> server renew-token reconnect -> client auth-ticket connect.

### Important operational note

- The relay is now **v2-capable**, but the system is **not yet globally switched to v2 by server-side policy alone**.
- In the current code, new share creation is still gated by the **host runtime** through `GGCODE_SHARE_PROTOCOL=v2` (`internal/tunnel/share_protocol.go` / `internal/tunnel/session.go`).
- The relay-side `GGCODE_SHARE_PROTOCOL` currently affects only the compatibility notice text; it does not force v2 issuance for hosts that did not request it.
- If v2 issuance fails today, the host still falls back to legacy compatibility mode. That is intentional for the current staged rollout, but it is not the final hard-cutover behavior.

### Future real cutover steps

1. Keep the relay in dual-stack mode while new TUI/desktop/mobile builds with dormant v2 support continue to ship.
2. Validate in real user/client traffic that:
   - legacy share still works for mixed-version users;
   - updated mobile can consume both legacy and v2 QR/link formats;
   - updated hosts can create and sustain v2 sessions reliably.
3. Perform a **soft cutover** by enabling `GGCODE_SHARE_PROTOCOL=v2` on the share-creating host side (or by changing the packaged default in a release) while leaving relay legacy handshake support online.
4. Monitor issuance failures, renew/reconnect failures, and the share-mode distribution between legacy/v2 before tightening policy.
5. Before the **hard cutover**, remove the host's silent fallback from v2 issuance failure to legacy compatibility and replace it with an explicit upgrade/service-availability notice.
6. Only after the client population is sufficiently upgraded, choose whether to:
   - continue allowing legacy websocket handshakes for old sessions only, or
   - reject new legacy share creation / legacy connections with a mandatory-upgrade message.
7. If future operations require a **relay-only switch** without touching host runtime config, add a follow-up control-plane change so hosts learn the desired share mode from relay/service policy instead of only from local `GGCODE_SHARE_PROTOCOL`.
