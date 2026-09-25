# Context Reset with Structured Handoff (`/handoff`)

## Problem

Long-running agentic sessions degrade as the context window fills. Frontier
harness practice (Anthropic, ["Harness design for long-running application
development"](https://www.anthropic.com/engineering/harness-design-long-running-apps),
Mar 2026) distinguishes two remedies:

| Mechanism | What it does | Limitation |
|-----------|--------------|------------|
| Compaction (`/compact`) | Summarizes history **in place**; same agent continues on a shortened history | No clean slate; "context anxiety" can persist |
| Context reset (`/clear`) | Clears context; fresh agent | **Zero handoff** — the new agent knows nothing |

ggcode had both ends but not their combination. The missing piece is the
**structured handoff artifact**: before the reset, distill the session into a
deterministic briefing (state + next steps) and seed the fresh session with it.

## Design

- **Zero LLM calls.** The artifact is built from session-local facts, so the
  reset is instant, deterministic and offline-safe:
  - *Mission*: the most recent ≤10 user goals (from session messages).
  - *Task board*: live board stats + digest; the board JSON itself is carried
    into the new session via `Session.TasksJSON` (same mechanism as `/branch`),
    so task IDs remain valid across the reset.
  - *Git snapshot*: branch / dirty files (≤30) / recent commits, best-effort.
- **Seeding**: `Session.ContextMessages` takes precedence over `Messages` in
  `RestoreSessionIntoAgent`, so setting it to a single `system` message makes
  the handoff the *entire* LLM context of the fresh session (protocol-safe:
  a lone system message, no tool_use/tool_result pairing concerns).
- **Artifact file**: written to `.ggcode/handoffs/handoff-<session>-<ts>.md`
  (best-effort) for human inspection and grep; the `[Session Handoff]` marker
  identifies handoff messages, mirroring the context manager's
  `[Previous conversation summary]` marker convention.
- **Session-switch choreography** mirrors `handleClearChat` (loading guard,
  sub-agent/swarm teardown, JSONL meta flush — never `Save()`), keeping the
  #541/#688 invariants intact.

## Files

- `internal/handoff` — pure artifact builder: `Snapshot`, `ExtractGoals`,
  `CollectGit`, `Render`, `SystemMessage`, caps and sanitizers.
- `internal/tui/handoff_command.go` — `handleHandoffCommand` (TUI wiring).
- Dispatch/completion/i18n (en+zh) registrations; docs table entry.

## Non-goals

- No LLM-generated narrative summary (deterministic facts only; compaction
  remains the tool for in-place summarization).
- No automatic triggering — reset stays user-initiated.
