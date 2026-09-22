# Prompt Caching (Anthropic)

## Overview

ggcode applies Anthropic prompt caching (ephemeral `cache_control` breakpoints)
to every Anthropic-protocol request. Prompt caching turns repeated prefix
processing into discounted cache reads, cutting both input cost and
time-to-first-token in long sessions. The strategy follows Anthropic's agentic
caching guidance and the "Don't Break the Cache" paper (arXiv:2601.06007).

Anthropic allows **at most 4 `cache_control` breakpoints per request**, and
breakpoints are positional: each one caches the request prefix up to and
including its block. ggcode plans all breakpoint spend up front with a budget
allocator instead of emitting them opportunistically.

## Breakpoint Budget Allocator

Priority order (highest cache value first):

| Priority | Target | Rationale |
|---|---|---|
| 1 | Static system prefix (`Cache: true` blocks) | Stable across the whole session |
| 2 | Conversation tail (last cacheable block of the last user message) | Incremental: caches system + tools + the entire history for the next turn; the history dwarfs the system prompt in long agent loops |
| 3 | Tool schemas (last non-deferred definition) | Static, large |
| 4 | Server tools / memory tool (trailing declaration) | Static; appended after regular tools so their breakpoint prefix-covers the regular ones |

Typical budgets:

- No server tools: system (1) + tail (1) + tools (1) = 3 of 4 used
- Server tools active: system (1) + tail (1) + server tool union (1) = 3 of 4
  used; the redundant regular-tools breakpoint is skipped
- Memory tool without server tools: system (1) + tail (1) + tools (1) + memory (1) = 4 of 4

If the budget would ever be exceeded (defensive), the tail breakpoint is
sacrificed first, then the redundant tool-schema breakpoint; hinted system
blocks keep priority.

## Tail Breakpoint Rules

The conversation-tail breakpoint is placed on the **last cacheable content
block of the last user message** (tool_result, text, or image). This is the
standard agentic pattern: each turn's request reuses the cache entry written
by the previous turn, so only the newly appended message is processed at full
input price.

Skipped deliberately:

- **Assistant-prefill tails** — prefill + cache interplay with extended
  thinking is subtle; caching the prefix up to the last user message is
  already covered by the previous turn's entry.
- **Unsupported block shapes** (thinking echoes, server tool result payloads) —
  the placement walks backwards to the previous block in the same message.

## Cache-Friendliness Elsewhere

- Only the static system base carries the `Cache: true` hint; dynamic layers
  (ratchet rules, playbooks) are emitted as separate un-hinted blocks so they
  never invalidate the static prefix.
- The temporal context header is anchored to session start, not sampled per
  invocation, so rendered bytes stay identical across iterations of a run.
- Context editing (clear_tool_results) rewrites history server-side, which
  naturally re-establishes the cache from the edit point onward.

## Observability

Cache hits are visible in session metrics: `cache read` tokens appear in
`/usage` output and the metrics summary (`TotalCacheRead`). A healthy agent
session should show cache-read tokens dominating input tokens after the first
few turns.
