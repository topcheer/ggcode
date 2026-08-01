# Agent Loop Design

This document describes the core agent loop and its deterministic optimization layers.

## Core Loop

The agent loop lives in `internal/agent/agent.go` and follows the standard tool-calling agent pattern:

```
user message → build system prompt → LLM call → parse tool calls →
execute tools (with permission checks) → inject guidance → feed results back → repeat
```

Key files:

- `agent.go` — `Agent` struct, `Run`/`RunStream`, provider orchestration, rate-limit awareness.
- `agent_tool.go` — tool execution, diff confirmation, pre/post hooks.
- `agent_autopilot.go` — autopilot continuation and goal-directed execution.
- `agent_compact.go` — reactive compaction, context budget analysis, fallback checkpoints.
- `agent_precompact.go` — progressive tool-result clearing and background precompact.
- `agent_prompt_inject.go` — dynamic system prompt injection (lanchat peers, playbook hints).

## Performance Optimization Stack

All optimization layers are deterministic and run in-process without extra LLM calls:

| Layer | File | Purpose |
|-------|------|---------|
| Speculative execution | `speculate.go` | Bigram-based prediction and pre-execution of likely next read-only tools (PASTE-inspired) |
| Tool memoization | `memoize.go` | LRU cache for read-only tool results with mtime/TTL invalidation (ToolCaching-inspired) |
| Parallel pre-execution | `parallel_tools.go` | Execute read-only tools from a batch concurrently (max 3) |
| Dynamic tool pruning | `tool/relevance.go` | Filter low-relevance MCP tools when total tool count >30, using BM25 relevance scoring (RAG-MCP-inspired) |
| Tool output guard | `tool_output_guard.go` | Progressive output truncation by context fill level |
| Superseded reads | `internal/context/manager.go` | Replace stale re-reads of the same file |
| Tool-result clearing | `agent_precompact.go` | Mechanical placeholder replacement at 50/65/75% fill |
| Tool-use input clearing | `agent_precompact.go` | Truncate old edit/write inputs after results are cleared |
| Reasoning block compaction | `internal/context/manager.go` | Clear old thinking/reasoning_content blocks |
| Adaptive effort | `adaptive_effort.go` | Per-turn reasoning effort adaptation based on tool complexity (Opus 5 effort toggle pattern) |
| Command caching | `command_cache.go` | Deterministic build/test command result caching |
| Prompt cache keepalive | `cache_keepalive.go` | Anthropic prompt-cache warming pings during idle (saves ~83K tokens on resume) |

## Trajectory Monitoring

The loop monitors its own trajectory and injects guidance when patterns look pathological:

| Monitor | File | Purpose |
|---------|------|---------|
| Loop detector | `loop_detect.go` | Exact duplicate calls and progressive error streak (4/7/10) |
| Overseer | `overseer.go` | Spam, read-only stall, stuck file, error escalation, drift (20/40/60 iterations) |
| Repetition tracker | `repetition_tracker.go` | Semantic-level failed-edit clusters on the same file |
| Confidence scorer | `confidence.go` | Holistic 6-signal trajectory confidence score (HTC-inspired) |
| Budget guard | `budget_guard.go` | Per-step token cost trend monitoring (BAGEN-inspired) |
| Error classifier | `error_classifier.go` | Type-specific guidance on first error (10 categories, AgentDebug-inspired) |
| Scope drift | `scope_drift.go` | Semantic scope creep detection via file-diversity tracking |
| Empty search spiral | `empty_search.go` | Detects futile search patterns and injects alternative strategies |
| Recurring error | `recurring_error.go` | Recurring build/test error fingerprint detection across edit cycles |
| Unread edit guard | `unread_edit.go` | Warns when editing files that haven't been read first |
| Edit fail recovery | `edit_fail_recovery.go` | Consecutive edit failure recovery guidance |
| Todo staleness | `todo_staleness.go` | Mid-run stale todo detection (plan abandonment awareness) |
| Latency tracker | `latency_tracker.go` | Per-tool latency baseline and slow-tool outlier detection |

## Tool Reliability

| Layer | File | Purpose |
|-------|------|---------|
| Transient retry | `transient_retry.go` | Automatic retry of idempotent read-only tools on transient errors (LSP timeout, network blips). Max 2 retries with 200ms/400ms backoff, 8 total per run. |
| Export guard | `export_guard.go` | Detects breaking changes to exported Go symbols (removed functions, changed signatures) by comparing against git HEAD. Fires once per file per run. |

## Failure Learning

| Layer | File | Purpose |
|-------|------|---------|
| Ratchet | `ratchet.go` + `ratchet_reactive.go` | Learned error rules matched proactively and reactively |
| Verify hint | `verify_hint.go` | Post-edit build reminders with smart reset on verify commands |
| Playbook | `playbook.go` | Strategy pattern learning from successful runs (ACE-inspired) |
| Reflection | `reflection.go` | Run-level self-assessment and insight recording |

## Pre-Completion Verification

Before the agent returns (no more tool calls), multiple gates check that the work is actually done:

| Gate | File | Purpose |
|------|------|---------|
| Incomplete todo check | `todo_check.go` | Detects unfinished todo items and injects reminders (max 2) |
| Fulfillment gate | `fulfillment_gate.go` | Zero-LLM-cost heuristic that verifies the agent's actual work matches the user's request. Detects: action-but-no-changes, wrong-file detection, multi-part coverage gaps. Fires at most once per run. |
| Sync verify | `verify.go` | Build/test verification with auto-repair on failure |
| Async verify | `verify.go` | Background lint check after successful build |

## Reliability

- `message_validation.go` — validates and repairs LLM message lists before sending to the provider.
- `context/budget.go` — context budget analyzer: breaks down token usage by category (system, history, tools) for diagnostics.
- `metrics/summary.go` — per-LLM-call TPS calculation with simple averaging across calls.
- `provider/rate_limit.go` — captures rate-limit headers from Anthropic/OpenAI responses for proactive quota awareness.

See also `docs/design/context-management.md` for context management details.
