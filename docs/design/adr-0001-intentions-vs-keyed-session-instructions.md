# ADR-0001: Prospective intention layer (memory.intentions.*) vs keyed session instructions (session.instructions.*)

Status: Accepted (2026-11-16)
Issues: #5260 (this layer), #5698 (keyed session instructions)
Component: internal/intentions, internal/tool/intention_tools.go

## Context

Two complementary persistence layers landed close together:

- **#5698 keyed session instructions** — *read-time* standing directives keyed
  into session context (e.g. "always run tests before committing"). They are
  injected into every relevant session on load.
- **#5260 prospective intentions** — *write-time* captured "when X happens,
  do Y" statements that stay dormant until an activation cue fires
  (file change, cron time, observed event), then surface in-session with
  full utterance provenance.

Without an explicit ruling the two blur: an intention could be injected as a
standing instruction (turning a deferred trigger into an always-on directive),
or a session instruction could be stored as an intention (turning standing
guidance into a one-shot trigger).

## Decision

1. **Namespace isolation.** Intentions persist under `memory.intentions.*`
   (single workspace-level store file `~/.ggcode/intentions/intentions.json`
   with an explicit `"namespace": "memory.intentions"` marker, cross-process
   flock sidecar). Keyed session instructions stay under
   `session.instructions.*`. Neither layer reads or writes the other's keys.
   Store loaders refuse files whose namespace marker mismatches.

2. **No injection.** Intentions are never injected into session context at
   read time. They surface only when their cue fires, via the same session
   emit path cron uses, and the fired prompt carries the verbatim
   origin excerpt plus an explicit realize/retire instruction. The engine
   never acts autonomously — firing only enqueues a prompt for the agent.

3. **Capture invariants.** `put` requires statement, cue family, cue pattern,
   origin session and origin excerpt (verbatim utterance). Duplicate
   suppression on (statement, family, pattern) among pending intentions.
   Lifecycle: `pending -> fired -> realized|retired` (fire-once default),
   `repeat` re-arms with a cooldown.

4. **Merge path (post-#5260).** If the keyed-instruction layer later grows a
   prospective surface (or intentions grow standing semantics), the merge is
   a *read-side join over two namespaces*, never a storage migration: a
   unified "agent commitments" view may read both stores, but each keeps its
   own write path, lifecycle and provenance model. Storage-level merging is
   explicitly rejected: the lifecycle mismatch (perpetual vs fire-once)
   makes a single schema lossy for one side or the other.

## Cue families

Shipped in this layer: `file` (glob change, FileWatchTrigger semantics:
prime-then-fire), `time` (cron expression via `cron.NextTime`), `event`
(case-insensitive substring over pushed observations — `Engine.Observe`,
exercised through the intention tool's `observe` action; production event
feeds such as IM/tool-result auto-push are the documented extension point).
A future poll-adapter family (deadline watchpoints, third-party webhooks)
registers as a new `CueFamily` case plus a poll hook — no schema change
(`cue_family`/`cue_pattern` already carry it).

## Consequences

- Cross-seat safety: concurrent seats sharing one `~/.ggcode` are serialized
  by the flock + read-under-lock discipline; a fired intention is re-checked
  for activity inside the mutation, so two seats cannot double-fire.
- Zero-config default behavior is unchanged: with no captured intentions the
  engine polls an empty store and emits nothing; wiring is additive at the
  three construction sites (root, daemon, wailskit).

## Keyed layer landed shape (#5698)

The keyed layer shipped as an agent-side store over **session storage**,
fully inside the `session.instructions.*` namespace:

- **Storage.** Every put/remove appends one immutable `instruction`
  record to the session JSONL (actor provenance + timestamp on every
  record — the append-only log doubles as the durable audit trail).
  Loading folds records last-wins per key, independently of the
  message/checkpoint replay paths, so entries survive revert, compaction
  and restarts. Older binaries skip unknown `instruction` lines and
  never rewrite existing lines, so mixed-version fleets are rewrite-safe
  on unknown fields.
- **Write gate.** `Agent.PutKeyedInstruction` /
  `Agent.RemoveKeyedInstruction` accept only `user` and
  `parent-agent` provenance; subagent-output, MCP-result,
  tool-result and model provenance are rejected *before any storage
  mutation* (persistent-injection-channel gate). Every attempt —
  including no-ops and rejections — leaves an audit line (in-memory ring
  + `debug.Log`; durable trail = record log).
- **Render.** Entries render as sorted, stable keyed blocks appended to
  the dynamic system prompt; only a changed entry's block rewrites, so
  the prefix cache stays valid for untouched blocks. Put/remove prepend
  exactly one delta line inside the changed entry's block at the next
  system-prompt assembly (announce bit consumed on shipment) — no extra
  model turn, no inbox wake.
- **User surface.** TUI /instructions put|remove|list` is the
  shipped user-path writer. The parent-agent writer is reserved for the
  spawn/subagent configuration surface (follow-up); the gate already
  accepts its provenance.

## Merge path refinement (keyed as sugar layer)

Per decision 4 above, storage-level merging stays rejected. The concrete
post-#5260 merge shape, should a single "agent commitments" UX ever be
wanted, is a **sugar layer**: the keyed-instruction *surface* (put/remove
by key, keyed block rendering) can be re-targeted to emit standing
intentions into the intention store, while the intention store gains an
always-on cue family for them — a read-side join over the two namespaces.
Each store keeps its own write path, lifecycle and provenance model; no
storage migration happens in either direction. Until that day the two
layers stay fully isolated as ruled above.
