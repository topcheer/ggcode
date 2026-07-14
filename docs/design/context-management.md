# Context Management Design

This document describes how ggcode manages conversation context, token budgets, and compaction.

## Context Manager

`internal/context/manager.go` implements `ContextManager` (imported as `ctxpkg` to avoid shadowing the standard `context` package). It stores `provider.Message` records, tracks estimated token usage, and performs mechanical + LLM-based compaction.

Key responsibilities:

- `Add(msg)` — append a message to the conversation.
- `TokenCount()` / `MessagesAndTokenCount()` — return current token estimate and messages.
- `RecordUsage(usage)` — calibrate token estimates against real provider usage.
- `Summarize(ctx, prov)` — LLM-based summarization of older conversation turns.
- `CheckAndSummarize(ctx, prov)` — check threshold and summarize if needed.
- `ReconcileToolCalls()` — ensure every `tool_use` has a matching `tool_result`.
- `CompactSupersededReads()` — replace stale re-reads of the same file.
- `ClearOldToolResults(keepN)` — replace old tool_result outputs with placeholders.
- `ClearOldToolUseInputs()` — truncate old edit/write Input arguments.
- `ClearOldReasoningBlocks(keepN)` — clear old thinking/reasoning_content blocks.
- `extractConstraints()` / `buildPostCompactState()` — preserve user constraint sentences across compaction.

## Token Estimation

`internal/context/tokenizer.go` provides `EstimateTokens(text string)` using a heuristic `len(text)/4` for ASCII, adjusted for CJK and code. `TokenCalibrator` (if present) uses `RecordUsage` feedback to self-calibrate the ratio per session.

## Compaction Pipeline

When context fills up, the following pipeline runs in order:

1. **Superseded reads compaction** — replace earlier reads of the same file with placeholders. Safest because the newer read already has current content.
2. **Tool-result clearing tiers** — at 50%, 65%, and 75% of the compaction threshold, progressively replace older `tool_result` outputs. Keeps the last `N` results intact (`12` / `8` / `4`).
3. **Tool-use input clearing** — truncate old `tool_use` Input arguments whose matching results have been cleared.
4. **Reasoning block compaction** — clear old `thinking`/`reasoning_content` blocks from assistant turns, keeping the last 3. Skips tool-call turns that require reasoning_content for correctness.
5. **Background precompact** — `agent_precompact.go` starts an LLM summarization in a background goroutine with a 6-second delay and 180-second timeout.
6. **Reactive compact fallback** — if precompact fails or context is still too high, `agent_compact.go` performs synchronous truncation as a fallback.

## Context-Fill-Aware Output Guard

`internal/agent/tool_output_guard.go` proactively truncates non-error tool outputs before they enter context, based on the current fill ratio:

| Fill level | Limit | Strategy |
|------------|-------|----------|
| < 50% | none | no truncation |
| 50–65% | 40 KB | head + tail preservation |
| 65–75% | 20 KB | head + tail preservation |
| 75%+ | 10 KB | head + tail preservation |

Error results are never truncated.

## Constraint Pinning

To prevent compaction from silently dropping user constraints, `extractConstraints()` scans user messages for sentences containing constraint keywords (`must`, `never`, `always`, `don't`, `important`, `constraint`, `rule`, etc., plus Chinese equivalents). These are deduplicated and re-injected as a system message after compaction via `buildPostCompactState()`.

## Persistent Storage

Session messages are persisted as JSONL files in `~/.ggcode/sessions/`. The context manager itself is in-memory; the session store restores messages into it on resume.

See also `docs/ARCHITECTURE.md` for the full subsystem layout and `docs/design/agent-loop.md` for agent loop details.
