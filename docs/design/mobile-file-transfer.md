# Mobile File Transfer (agent → mobile, any format)

Status: Proposed (watchdog-reviewed design baseline)
Owner: unassigned (implementation to be delegated)
Scope: V1 as described here — desktop (Go) + mobile (Flutter) only, **no relay gateway changes**.

## Problem

The mobile app can *send* images to the agent (inbound `images` field on user
messages, `protocol.dart:MessageImage`), but there is no outbound path for the
agent to deliver files (screenshots, logs, PDFs, archives, any format) back to
the phone. `message_bubble.dart` has no attachment rendering at all.

## Constraints (verified in code, 2026-10-02)

| Constraint | Evidence |
|---|---|
| No HTTP file endpoint on relay gateway | `ggcode-relay/relay.go:1908-1920` route table: only `/ws`, `/share/session*`, model-catalog, health/stats/nuke |
| WS frame budget ~1MB | desktop reads at `SetReadLimit(1 << 20)` (`internal/tunnel/relay_client.go:377`); all payloads ride an E2E-encrypted `encrypted` envelope (nonce+ciphertext are base64 strings) |
| Gateway is independently deployed | `ggcode-relay/deploy.sh` — changing it is out of scope for V1 |
| Existing event vocabulary | `internal/tunnel/protocol.go` event consts; flutter mirrors in `WsMessage.type` dispatch |

## Design

### Transport: chunked inline over the existing encrypted WS channel

Avoids gateway changes entirely. Base64-in-JSON costs ~33%; with a 512 KiB
raw chunk the encrypted envelope stays well under the 1 MiB read limit.

Two new server→client event types (lowercase, per existing convention):

```
file_offer    {file_id, filename, mime, size, sha256, chunks}
file_chunk    {file_id, index, data}   // data = base64(raw chunk)
file_done     {file_id}                // optional terminator; success inferred from chunks received + sha256
```

- `file_id`: host-generated opaque ID (uuid/hex), scoped to the session.
- `sha256`: over the whole raw file; client verifies before surfacing.
- Chunk raw size 512 KiB fixed (last chunk may be short).
- Offers are sent *before* their chunks; chunks for one file are enqueued
  back-to-back but the sender MUST yield (interleave any pending interactive
  frames) so streaming text is not starved. Simplest correct rule: enqueue at
  most 1 chunk per 50 ms, or drain-to-empty between chunks when the queue is
  otherwise idle.
- Single-file cap 50 MiB (chunk count sanity: `chunks <= ceil(50MiB/512KiB)`),
  rejected locally with an explanatory message before any bytes are sent.
- Failure/retry: V1 does not retransmit. If the socket drops mid-transfer the
  client discards partial state on `snapshot_reset`/reconnect; host re-offers
  are idempotent by `file_id` (client replaces partial state).

### Desktop (Go) side

- New `internal/tunnel` API: `SendFileOffer`/`SendFileChunk` shaped like the
  existing `Send*` helpers; both ride `GatewayMessage{Type, Data}`.
- New agent-facing capability wired the same way the IM `send_file` action
  works (`im` tool): a `send_file_to_mobile` tool (or an option on the
  existing mobile/tunnel tool surface — implementer's choice, keep it
  discoverable) taking `path` + optional `caption`. It reads the file,
  hashes, chunks, and drives the two Send helpers with the queue-yield rule.
- Filename sanitization: strip path separators (`/`, `\`, NUL), clamp to 255
  UTF-8 bytes, never send absolute paths.
- MIME: sniffed via existing `internal/extract`/`http.DetectContentType` with
  extension fallback; `application/octet-stream` default. No allowlist —
  "any format" is the requirement.

### Mobile (Flutter) side

- `protocol.dart`: `FileOfferData`, `FileChunkData` models + parse helpers.
- Transfer state in a provider (Map<file_id, {meta, received bytes/blocks,
  buffer}>); on completion: sha256 verify → write to app documents dir
  (`<docs>/received/<sanitized-filename>`, collision → `name (2).ext`) →
  surface as a finished file card.
- `message_bubble.dart`: new card widget for file messages — icon by
  mime/extension, filename, human size, progress (`n/chunks`) while
  transferring, done-state tap action = share sheet (`share_plus`) plus
  "Save to Files" (iOS) / save to Downloads (Android). Preview via
  system viewer (`open_file`/`file_preview`) for previewable types; other
  types still offer share/save (never in-app execute).
- Wire into the outbound path too: the *existing* inbound `images` field and
  the new outbound file events share the bubble's attachment area, so both
  directions render consistently.

### Security

- All chunks inherit the existing E2E encryption envelope — no new plaintext.
- Client MUST NOT resolve paths from `filename`; it is display metadata only.
- sha256 gate: unverified files are never written or surfaced.
- 50 MiB cap prevents memory abuse; chunk assembly uses a preallocated
  buffer sized from `size`, validated against the cap before allocation.

### Out of scope (V2 hooks noted in code comments)

- HTTP direct upload endpoint on the gateway for large files (>50 MiB) and
  resumable transfers.
- Resume across reconnects (needs gateway state).
- Inbound arbitrary files (phone → agent): separate proposal; current
  `images` field stays the inbound path for now.

## Acceptance checklist (the reviewer's gate)

1. Desktop builds (`go build -tags goolm ./...`) and tunnel tests pass,
   including a new test: file → offer+chunks framing, sha256, yield rule,
   filename sanitization, 50 MiB rejection.
2. Flutter `flutter analyze` clean; new widget/provider tests for assembly,
   sha256 mismatch discard, collision rename.
3. E2E on simulator: host sends a JPEG, a .zip, and a .log; all three render
   as cards, sha256-verified, shareable/savable; a >50 MiB file is refused
   with a clear message; text streaming during a transfer shows no visible
   stall (>200 ms gap).
4. No relay gateway code touched (`git diff --stat` proves scope).
