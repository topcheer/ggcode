# Mobile gateway incremental resume design

> **Status:** Implemented on `main`. The **Problem statement** section below records the pre-migration reconnect architecture that existed before the event-log / cursor-resume rollout (`sentLog`, `ReplayToClient()`, `chat_clear` reconnect replay). The protocol and phase sections describe the architecture that replaced it.

## Summary

Redesign the mobile tunnel/gateway protocol so reconnects are **ordered, idempotent, and incremental** instead of replaying and re-rendering the full session.

The new design treats the tunnel as a **session-scoped event log**:

- every server event has a stable, session-unique `event_id`
- the same logical event keeps the same `event_id` across replay, reconnect, and app restart
- multiple clients can attach to the same session and hold independent replay cursors
- mobile persists the last applied event cursor and only requests the missing gap
- relay/gateway only sends incremental events after that cursor
- full snapshot rebuilds happen only on explicit `resume_miss` / `snapshot_reset`

This replaces the current behavior where reconnect can replay the relay cache, broker log, and session history together, causing duplicate rendering, ordering drift, and unnecessary bandwidth usage.

## Problem statement

Current reconnect behavior is built from several overlapping mechanisms:

- `ggcode-relay/main.go` caches raw encrypted messages per room and replays them to newly connected clients before live traffic.
- `internal/tunnel/broker.go` also stores a `sentLog` and can `ReplayToClient()`.
- `internal/tui/tunnel.go` currently calls `ReplayToClient()` on reconnect and then pushes fresh `session_info` plus `chat_history`.
- mobile (`mobile/flutter/lib/core/providers/session_provider.dart`) mostly builds UI state by appending messages and matching tool results heuristically, not by consuming a stable event log.

This creates four user-visible problems:

1. reconnect can trigger multiple overlapping full replays
2. the same logical event has no stable identity beyond one transport pass
3. tool results and streaming text can be reattached to the wrong UI item after replay
4. mobile pays the cost of full-history retransmission and full redraw even when only a few events were missed

## Goals

- Guarantee in-session event idempotency across reconnect, replay, background resume, and app restart.
- Guarantee ordered rendering on mobile without depending on timing or "last matching item" heuristics.
- Minimize bandwidth by replaying only missing events.
- Keep payload contents encrypted end-to-end.
- Let mobile preserve local UI state during reconnect without flashing empty state.
- Support app restart recovery, not just same-process reconnect.
- Support multiple concurrent clients for the same session while keeping the upstream server unaware of per-client state.
- Let a late-joining client receive the full session history for the active session from the gateway.

## Non-goals

- Backward compatibility with older mobile tunnel protocol versions. The mobile app is not formally released yet.
- Building a generic cross-session sync product beyond one session-scoped event log with per-client cursors.
- Making relay inspect or interpret business payload contents.
- Persisting an infinite event log on the server. A bounded replay window is sufficient.

## Core invariants

These are hard requirements for implementation and tests:

1. A session has one stable `session_id`.
2. A server event has one stable `event_id`.
3. Replaying the same event must reuse the same `event_id`, never allocate a new one.
4. Mobile stores applied state keyed by `(session_id, event_id)`.
5. If mobile receives an already-applied `(session_id, event_id)`, it must drop it without changing UI state.
6. `event_id` ordering is the source of truth for replay and dedupe within one session.
7. Each connected client has its own independent cursor for the same `session_id`.
8. A newly attached client with no cursor can request full history for the active session from the gateway.
9. Incremental replay is the default reconnect path.
10. Full reset is only allowed through an explicit `snapshot_reset`.

## Proposed protocol model

### Outer relay envelope

The relay envelope is visible to the relay/gateway and is **not** part of the encrypted business payload.

```json
{
  "session_id": "sess-20260521-abc123",
  "event_id": "ev-00000125",
  "stream_id": "msg-17",
  "type": "message_delta",
  "nonce": "...",
  "ciphertext": "..."
}
```

Fields:

- `session_id`: stable ID for one ggcode conversation session
- `event_id`: globally ordered event identity within the session
- `stream_id`: logical stream grouping key, such as one assistant message stream or one subagent text stream
- `type`: high-level event kind for routing and replay decisions
- `nonce` / `ciphertext`: encrypted payload, unchanged in principle from current AES-GCM transport

### Encrypted payload

The encrypted payload carries the actual business data for the event:

- text chunks
- tool call open/result data
- approval / ask_user content
- subagent and teammate events
- session metadata
- snapshot items

Relay/gateway only needs envelope metadata for replay, resume, ordering, and caching.

### Encryption boundary

The envelope fields are deliberately **plaintext metadata** visible to relay/gateway:

- `session_id`
- `event_id`
- `stream_id`
- `type`
- transport framing fields such as `nonce`

Only `ciphertext` is end-to-end encrypted business content.

This keeps replay logic simple without requiring relay to inspect decrypted payloads. E2E guarantees apply to message contents, tool payloads, approvals, and history data inside `ciphertext`, not to outer routing metadata.

## Event model

The tunnel should transmit stable events, not UI-shaped render instructions.

### Message stream events

- `message_open`
- `message_delta`
- `message_close`

Used for:

- main assistant output
- subagent output
- teammate output

`stream_id` identifies the logical message. `message_open` creates the stream, `message_delta` carries only incremental text, and `message_close` finalizes the stream. Any later delta for a closed `stream_id` must be dropped as invalid or stale replay noise.

### Tool lifecycle events

- `tool_call_open`
- `tool_call_update`
- `tool_call_close`

Each carries a stable `tool_id`. Tool results are bound to `tool_id`, not inferred by `tool_name` or "last pending card".

### State events

- `session_info`
- `status`
- `approval_request`
- `approval_result`
- `ask_user_request`
- `ask_user_response`
- `subagent_spawn`
- `subagent_status`
- `subagent_complete`

These are naturally idempotent overwrite/update events instead of append-only chat rows.

### Snapshot control events

- `resume_miss`
- `snapshot_reset`
- `snapshot_item`
- `snapshot_end`

These are rare. They exist only to recover when the client's cursor falls outside the retained replay window or local cache is invalid.

## Resume and replay flow

### Client persistent state

Mobile persists:

- `client_id`
- `session_id`
- `last_applied_event_id`
- a bounded local event log window
- a materialized chat/subagent/tool view

On app restart, mobile restores the materialized view immediately and then attempts incremental resume.

### Handshake

After transport connection is established, client sends a cleartext resume hello:

```json
{
  "type": "resume_hello",
  "client_id": "mobile-device-uuid",
  "session_id": "sess-20260521-abc123",
  "last_event_id": "ev-00000125"
}
```

`client_id` is required because gateway must track multiple clients independently even though the upstream server stays client-unaware. `last_event_id` is optional:

- if present, gateway performs incremental replay after that cursor for this client
- if absent, gateway treats the connection as a fresh attach and replays the full active-session history to that client

### Resume response mode

Gateway should acknowledge `resume_hello` with an explicit mode instead of forcing mobile to infer fallback behavior indirectly.

Example:

```json
{
  "type": "resume_ack",
  "session_id": "sess-20260521-abc123",
  "client_id": "mobile-device-uuid",
  "resume_mode": "incremental"
}
```

`resume_mode` values:

- `incremental`: the requested cursor is satisfiable and gateway will replay only the missing gap
- `full_history`: this is a fresh attach with no cursor, and gateway will replay the full active-session history
- `snapshot_required`: the requested cursor cannot be satisfied from gateway-held history, so the client must switch to snapshot fallback

### Successful resume

If gateway responds with `resume_mode: "incremental"`:

1. gateway locates `last_event_id`
2. only events after that cursor are replayed
3. client applies the missing gap
4. UI stays intact and only new content appears

### Resume miss

If gateway responds with `resume_mode: "snapshot_required"`:

1. gateway sends `resume_miss`
2. gateway sends `snapshot_reset`
3. gateway streams a fresh snapshot with stable snapshot event IDs
4. client replaces its local projection in a controlled way
5. client shows a lightweight "Session resynced" style notification

The system must never silently fall back to full replay or `chat_clear` on ordinary reconnect.

## Relay responsibilities

`ggcode-relay` should stop acting as an opaque raw-message cache only.

Instead, each room should maintain:

1. a **session event log** for the active session
2. a **client registry** keyed by `client_id` with per-client cursor state

The session event log stores envelopes with metadata:

- `session_id`
- `event_id`
- `stream_id`
- `type`
- raw encrypted body

Responsibilities:

- preserve strict FIFO ordering inside one session log
- answer `resume_hello(client_id, last_event_id)` using that client's cursor semantics
- replay full active-session history when a client attaches without `last_event_id`
- replay only the gap when `last_event_id` is present and still satisfiable
- keep per-client delivery state without requiring the upstream server to know about clients
- return `resume_miss` when the requested cursor cannot be satisfied from gateway-owned history

### Ordering guarantees

Gateway must guarantee **per-client ordering**, not cross-client lockstep delivery:

- each `client_conn` must observe strictly increasing `event_id` order
- a slow client may lag behind a fast client
- client A receiving `ev-100` before client B does is acceptable
- client B receiving `ev-100` before `ev-99` is not acceptable

The practical rule is: one ordered outbound queue per `client_conn`, with backpressure and replay handled independently for each client.

### History retention

For **active sessions**, gateway must retain enough data to reconstruct full history for a late-joining client. The simplest correct v1 is to retain the full active-session event log until the session ends.

Later, this can be optimized into a gateway-owned `snapshot + tail` model, but the semantics must stay the same: a second client connecting at event 200 must still be able to reconstruct events `1..200` without the upstream server tracking that client.

Recommended initial limits:

- client cache: last 500-1000 events or a conservative byte cap
- gateway in-memory tail: configurable, but not the source of truth for active-session history
- active-session history: retained for the session lifetime, with optional spill-to-disk if needed

Client-side limits are mainly about local dedupe and resume efficiency. Gateway-side active-session retention is a correctness requirement for multi-client attach.

## Broker responsibilities

`internal/tunnel/broker.go` should become the single event producer for tunnel output.

### Required changes

1. Replace `sentLog []GatewayMessage` with a stable `eventLog []EnvelopeEvent`.
2. Allocate `event_id` exactly once in `appendEvent()` / `enqueueEvent()`.
3. Preserve that `event_id` during all replay paths.
4. As an immediate cleanup step, remove broker-side reconnect replay paths (`ReplayToClient()` and reconnect-triggered `PushChatHistory()`) so reconnect no longer has triple-replay behavior while Phase 2 is being built.
5. Treat full snapshot generation as an explicit fallback path, not part of normal reconnect.

### Important consequence

The current reconnect logic in `internal/tui/tunnel.go` must change:

- remove the reconnect-time `ReplayToClient()` + `PushChatHistory()` pattern
- replace it with resume-aware behavior driven by cursor state

This is the core fix for current duplicate/all-history replay behavior.

## Mobile client architecture

Mobile should consume tunnel data as an event log and then project it into UI state.

### Two-layer local state

1. **Event log**
   - append-only bounded log of recent `(session_id, event_id)` entries
   - persisted for resume and dedupe

2. **Materialized view**
   - projected chat list
   - projected tool cards
   - projected subagent / teammate panel state
   - stable stream buffers for ongoing text streams

### Matching rules

- text streams match on `stream_id`
- tool events match on `tool_id`
- dedupe matches on `(session_id, event_id)`
- ordering is defined by event log order, not local arrival heuristics

### Rendering rules

- normal reconnect must not clear the screen
- duplicate `event_id` must be ignored
- if mobile sees an `event_id` gap relative to the last applied cursor, it should request replay from the last good cursor instead of trying to locally reorder speculative state
- `chat_clear` should no longer be used as reconnect default; only `snapshot_reset` may trigger a controlled rebuild

### New client attach rules

- a client with no local cursor is a fresh attach, not a reconnect
- fresh attach should replay full active-session history in order
- this replay must use the same stable `event_id`s as existing clients already saw
- fresh attach for client B must not disturb client A's cursor or projection state

## User experience requirements

- On disconnect/reconnect, keep the existing screen visible and show a small reconnect indicator.
- On successful incremental resume, do not show a heavy notification.
- On `snapshot_reset`, show one small resync hint.
- Streaming text that resumes after reconnect must continue in the same message bubble or subagent panel entry.
- Tool calls/results must preserve pairing across reconnects.

The target user experience is a cached messaging client, not a repeatedly mirrored terminal session.

## Failure handling

### Duplicate event

If `(session_id, event_id)` already exists locally, drop it.

### Broken chain

If mobile detects an `event_id` gap:

1. do not mutate the projection out of order
2. request `resume_from(last_good_event_id)`
3. if replay cannot satisfy the gap, fall back to `snapshot_reset`

### Missing tool parent

If a tool result arrives before its tool call has been projected:

- hold it in a pending map keyed by `tool_id`
- attach it once the corresponding tool open event arrives

### Cache corruption

If local event log and materialized view disagree:

- prefer replay from the last trustworthy cursor
- if that fails, use `snapshot_reset`

## Implementation plan

### Phase 1: stable event identity

- add `session_id`, `event_id`, `stream_id` to the tunnel envelope
- make broker emit stable event IDs
- remove reconnect-time broker `ReplayToClient()` and `PushChatHistory()` so only one replay path remains during the migration
- add gateway-side `client_id` tracking with independent per-client cursors
- persist `last_applied_event_id` on mobile
- make mobile dedupe by `(session_id, event_id)`

### Phase 2: incremental resume

- add `resume_hello`
- add gateway-side per-client gap replay
- add fresh-attach full-history replay for clients with no cursor
- replace the remaining relay raw-cache replay with cursor-based replay semantics and keep full snapshot reconstruction off the ordinary reconnect path

### Phase 3: explicit snapshot fallback

- implement `resume_miss`, `snapshot_reset`, `snapshot_item`, `snapshot_end`
- move full-history reconstruction behind that path only

### Phase 4: client projection cleanup

- remove remaining UI matching heuristics based on local counters / lastIndexWhere fallbacks where stable IDs are available
- ensure tool and stream projections are fully driven by stable identifiers

## Test strategy

### Unit tests

- event IDs are monotonic and stable across replay
- duplicate events are dropped idempotently
- resume from cursor returns only the missing gap
- fresh attach with empty cursor returns full active-session history
- expired cursor returns `resume_mode: "snapshot_required"`
- `resume_miss` triggers snapshot fallback

### Integration tests

1. Normal live session with streaming text
2. Disconnect mid-stream, then resume successfully
3. App kill/restart, then resume from persisted cursor
4. Second client attaches mid-session and receives full ordered history without affecting the first client
5. Cursor outside satisfiable gateway history, requiring `snapshot_reset`
6. Two simultaneously connected clients both preserve per-client monotonic ordering even when one client's socket is slow
7. Tool call/result pairing remains correct across reconnect
8. Subagent and teammate text/tool streams remain ordered and non-duplicated

### Regression checks

- reconnect after a short interruption should render roughly the missed gap, not full history
- a new client joining mid-session should receive full history for that session
- reconnect should not issue default `chat_clear`
- one `(session_id, event_id)` must produce at most one applied UI mutation

## Open implementation notes

- GUI and TUI producers should use the same broker/event-log design where they feed mobile through the gateway path.
- Existing `seq` can remain temporarily as a transport/debug field, but it is no longer the source of truth for resume or dedupe.
- The relay should not need to inspect decrypted payload contents. All replay logic should rely on envelope metadata only.

## Recommendation

Implement the new protocol directly without compatibility shims.

Because the mobile app is not formally released yet, the cleanest path is:

- adopt stable event IDs now
- delete reconnect-time full replay behavior
- switch mobile to cursor-based incremental resume
- reserve full snapshot rebuilds for explicit fallback only

That gives the best user experience, the lowest bandwidth cost, and the simplest long-term protocol model.
