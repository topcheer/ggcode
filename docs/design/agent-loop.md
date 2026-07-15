# Agent Loop Design

This document describes the core agent loop and its deterministic optimization layers.

## Core Loop

The agent loop lives in `internal/agent/agent.go` and follows the standard tool-calling agent pattern:

```
user message → build system prompt → LLM call → parse tool calls →
execute tools (with permission checks) → inject guidance → feed results back → repeat
```

Key files:

- `agent.go` — `Agent` struct, `Run`/`RunStream`, provider orchestration.
- `agent_tool.go` — tool execution, diff confirmation, pre/post hooks.
- `agent_autopilot.go` — autopilot continuation and goal-directed execution.
- `agent_compact.go` — reactive compaction and fallback checkpoints.
- `agent_precompact.go` — progressive tool-result clearing and background precompact.
- `agent_prompt_inject.go` — dynamic system prompt injection (lanchat peers, playbook hints).

## Optimization Stack

All optimization layers are deterministic and run in-process without extra LLM calls:

| Layer | File | Purpose |
|-------|------|---------|
| Speculative execution | `speculate.go` | Bigram-based prediction and pre-execution of likely next read-only tools |
| Tool memoization | `memoize.go` | LRU cache for read-only tool results with mtime/TTL invalidation |
| Parallel pre-execution | `parallel_tools.go` | Execute read-only tools from a batch concurrently (max 3) |
| Tool output guard | `tool_output_guard.go` | Progressive output truncation by context fill level |
| Superseded reads | `internal/context/manager.go` | Replace stale re-reads of the same file |
| Tool-result clearing | `agent_precompact.go` | Mechanical placeholder replacement at 50/65/75% fill |
| Tool-use input clearing | `agent_precompact.go` | Truncate old edit/write inputs after results are cleared |
| Reasoning block compaction | `internal/context/manager.go` | Clear old thinking/reasoning_content blocks |

## Trajectory Monitoring

The loop monitors its own trajectory and injects guidance when patterns look pathological:

- `loop_detect.go` — exact duplicate calls and progressive error streak (4/7/10).
- `overseer.go` — spam, read-only stall, stuck file, error escalation, drift (20/40/60 iterations).
- `repetition_tracker.go` — failed-edit clusters on the same file.
- `confidence.go` — holistic 6-signal trajectory confidence score.
- `budget_guard.go` — per-step token cost trend monitoring.
- `error_classifier.go` — type-specific guidance on first error (10 categories).

## Failure Learning

- `ratchet.go` + `ratchet_reactive.go` — learned error rules matched proactively and reactively.
- `verify_hint.go` — post-edit build reminders with smart reset on verify commands.
- `playbook.go` — strategy pattern learning from successful runs.
- `reflection.go` — run-level self-assessment.

## Reliability

- `message_validation.go` — validates and repairs LLM message lists before sending to the provider (especially after loading old sessions without checkpoints).
- `cache_keepalive.go` — Anthropic prompt-cache warming pings during idle.

See also `docs/ARCHITECTURE.md` for the full subsystem layout and `docs/design/context-management.md` for context management details.
