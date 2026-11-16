# Intentions (prospective intention layer)

The intention layer captures **deferred** commitments — "when X happens, I
should do Y" — at write time, stores them under the `memory.intentions.*`
namespace, and surfaces them in-session only when a cue actually fires. It
complements keyed session instructions (standing, read-time directives); the
isolation and merge rules are specified in
[ADR-0001](../design/adr-0001-intentions-vs-keyed-session-instructions.md).

## The `intention` tool

| Action | What it does |
|---|---|
| `put` | Capture an intention: statement, cue (family + pattern), verbatim `origin_excerpt` and optional `origin_session`. Duplicate pending intentions with the same statement+cue are rejected. |
| `list` | List stored intentions (active by default; `include_terminal` for realized/retired). |
| `realize` | Mark an intention done (its goal was accomplished). |
| `retire` | Mark an intention obsolete without doing it. |
| `observe` | Feed one event observation to event-family intentions (surface tag + text). |

## Cue families

- **file** — fires when files matching a glob change (mtime+size
  fingerprint, prime-then-fire so capture never immediately fires; mirrors
  the file-watch trigger semantics).
- **time** — fires at the next time matching a cron expression.
- **event** — push-based: fires when an `observe` call's text matches the
  cue pattern (case-insensitive substring).

## Behavior

- **Lifecycle**: `pending -> fired -> realized | retired`.
- **Fire-once by default**; `repeat: true` re-arms after a cooldown
  (`cooldown_sec`, default 10s) to suppress event storms.
- **Surfacing, not acting**: a fired intention is enqueued into the session
  as a prompt carrying its full provenance (statement, cue, origin excerpt)
  plus an explicit realize-or-retire instruction. The engine never edits
  code or takes actions autonomously.
- **Storage**: workspace-level single file `~/.ggcode/intentions/intentions.json`
  with cross-process locking, so multiple seats sharing a workspace do not
  double-capture or double-fire.
