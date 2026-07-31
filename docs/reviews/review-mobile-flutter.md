# Mobile Code Review — mobile/flutter (current state)

**Reviewer**: subagent (deepseek-v4-flash, same model as main agent)
**Scope**: full review of current mobile codebase (Flutter/Dart + stock Android Kotlin / iOS Swift), not a diff review
**Date**: 2026-08-01

## Verdict: SHIP WITH FIXES — blocking on C1 (auth-token storage)

Review counts: CRITICAL 1 · HIGH 3 · MEDIUM 6 · LOW 4

Chat/network core is well-engineered (resume/ack/dedup, gap recovery, exponential-backoff reconnect, ordered per-message queue, generation-guarded subscriptions, timer/stream cleanup). No correctness races or crash paths in primary paths. Main risks: credential storage, per-chunk snapshot write amplification, missing trust anchor for QR E2E key.

---

## CRITICAL

### C1. Auth tokens persisted in plaintext in multiple stores
- **Evidence**: `connection_provider.dart:1710` `_saveUrl` writes full runtime URL incl. `renew_token`/`auth_ticket` to SharedPreferences `ggcode_history` (1301-1309); `connection_store.dart:15,124` `StoredConnection.url` persists token-bearing URL to `ggcode_connections`; `workspace_cache.dart:392` SQLite `cache_sessions.url` + `2046` `_persistIndex`; `connection_service.dart:192-194` descriptor carries `authTicket`/`renewToken`/`serverPublicKey`
- **Deps**: pubspec has `shared_preferences` + `sqlite3`, **no** `flutter_secure_storage`/Keychain/Keystore
- **Why**: renew_token grants reconnection to a live room; with `kx_pub` an attacker completes v3 key exchange and drives the host agent (arbitrary code on host). Extracted from rooted/jailbroken devices, unencrypted backups, adb backup.
- **Fix**: store `auth_ticket`/`renew_token`/`kx_pub` in platform secure storage; persist only token-stripped `publicUrl`; rebuild runtime URL from secure storage on connect; purge tokens from `ggcode_history`.

---

## HIGH

### H1. Per-streamed-chunk full-snapshot write amplification on the main isolate
- **Evidence**: `main.dart:225-227` `persistLiveProjection()` on every chat state change; `chat_provider.dart:307-333` `handleTextChunk` rebuilds entire `List<ChatMessage>` per chunk; `workspace_cache.dart:1942-1948` 350ms debounce → `_flushDirtyState` serializes ALL messages (incl. base64 thumbnails) → `upsertSnapshot` (631-655) `DELETE + INSERT`
- **Why**: streaming hot path: full-list JSON + SQLite rewrite every ~350ms on UI isolate; jank, battery drain, O(n) `indexWhere` + spread per chunk
- **Fix**: dirty only on durable events, throttle 1-2s, skip-if-unchanged, offload JSON+SQLite to background isolate

### H2. No out-of-band trust anchor: E2E server key in-band via QR + no TLS pinning
- **Evidence**: `connection_service.dart:188` `serverPublicKey` from QR `kx_pub`; `tunnelUrlSecurityError` (54-65) permits `wss://` to any host, `ws://` to private/local; `WebSocket.connect` (356) no cert pinning
- **Why**: crafted QR fully controls AES-GCM key; E2E as strong as QR channel; client can drive host agent
- **Fix**: user-visible trust warning on first connect per room; pin relay CA/cert; never `ws://` except flagged localhost

### H3. No message-list pagination/truncation — unbounded memory + persistence growth
- **Evidence**: `chat_screen.dart:464` full list retained in memory + SQLite snapshot; only `cleanupOldSessions` (7-day, `workspace_cache.dart:702`) bounds sessions, not single long session
- **Why**: multi-hour session grows memory/snapshot monotonically; slow restart (full rehydration)
- **Fix**: window/cap in-memory list, paginate history from SQLite, cap snapshot payload

---

## MEDIUM

- **M1** `chat_provider.dart:206` — 5s ack-timeout `Future.delayed` never tracked/cancelled; accumulate on rapid sends. Fix: store Timer, cancel in clearMessages/dispose.
- **M2** `connection_service.dart:1101-1108` — P2P DataChannel carries unencrypted application payload (DTLS only); weaker than authenticated relay path. Fix: re-encrypt with tunnel key or document trust boundary.
- **M3** `connection_service.dart:734-739` — persistent decrypt error silently drops messages (only first 3 surfaced). Fix: force reconnect + resume on repeated decrypt failure; visible "messages may be missing" state.
- **M4** `connection_service.dart:18-19,54-65` — `ws://` allowed for LAN/private hosts; metadata unencrypted. Treat as dev-only; prefer `wss://`.
- **M5** `chat_provider.dart:177-179`, `input_bar.dart:48-75` — full base64 image per message, re-persisted every flush. Fix: separate small display thumbnail.
- **M6** `background_connection_manager.dart:75-81,90-99`, `connection_provider.dart:2237,1708` — unhandled async save chains; failing save silently swallowed. Fix: try/catch + reportError.

---

## LOW

- **L1** `workspace_cache.dart:637` — `DELETE` in `upsertSnapshot` not scoped by `workspace_key` (benign today; cross-workspace collision would clobber)
- **L2** `connection_service.dart:565` — heartbeat assumes prompt pong; busy host >60s may cause unnecessary reconnect (resume preserved, minor)
- **L3** `chat_provider.dart:206`, `connection_service.dart:515` — main-isolate JSON decode of large frames blocks UI
- **L4** Native layer entirely stock; no `usesCleartextTraffic`/ATS declaration; `ws://` LAN behavior needs on-device confirmation on both platforms

---

## Top 3 before release
1. **C1** — plaintext auth-token persistence (renew_token + kx_pub → host agent takeover) — MUST FIX
2. **H1** — per-chunk full-snapshot writes on UI isolate (jank/battery/DB churn)
3. **H2** — no out-of-band trust anchor / no TLS pinning (QR-swap attack surface)
