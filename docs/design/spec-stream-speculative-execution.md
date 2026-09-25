# Intra-Decode Speculative Tool Execution (spec_stream.go)

## Problem

An agent turn alternates between LLM decode and tool execution. ggcode already
attacks tool latency in two windows:

| Window | Mechanism | File |
|--------|-----------|------|
| Between LLM turns | Pattern-based (PASTE) speculation during model generation | `speculate.go` |
| After full decode | Concurrent batch execution of read-only calls (LLMCompiler / W&D) | `parallel_tools.go` |

Both leave the **intra-decode** window open: while a response is streaming, a
tool call's arguments are fully known the moment its `StreamEventToolCallDone`
event arrives — but execution still waits for the model to finish decoding the
rest of the response (prose, reasoning, further tool calls). On multi-call
turns with long text tails that is seconds of idle tool I/O latency.

## Concept

Client-side speculative tool execution:
- "Optimizing Agentic Language Model Inference via Speculative Tool Calls" (arXiv:2512.15834)
- "Speculative Actions: A Lossless Framework for Faster Agents" (arXiv:2510.04371)

The tool starts executing *while the model keeps decoding*; the latency of
short reads (read_file, grep, git_*) is hidden behind the decode tail, and the
mean trajectory latency shrinks without any provider-side changes.

## Implementation

`internal/agent/spec_stream.go` — `streamSpeculator`, one instance per LLM
turn (created in `RunStreamWithContent`, passed into `streamChatResponse`):

1. On each `StreamEventToolCallDone` for a tool in `speculativeSafeTools`
   (read-only, idempotent), a goroutine executes the call immediately via the
   same `preExecOne` path the batch pre-executor uses (30s timeout, bounded
   concurrency `specMaxConcurrent`).
2. When the stream finishes, `collect()` resolves all futures into the same
   `preExecuted` map the batch path fills, so the sequential loop consumes
   them through `usePreExecutedWithPermission` — permission gating (#1496),
   approval memory, metrics and post-execution detectors are unchanged.
3. Arrival order == index in the response's `toolCalls` slice, so keys match
   the sequential loop's index.

### Safety model

- **Read-only only.** Non-safe tools never speculate.
- **Conflict invalidation.** A later mutating call *in the same response*
  invalidates pending speculations:
  - file-scoped mutators (`edit_file`/`write_file`/`multi_edit_file`/
    `notebook_edit`): precise invalidation via `readAffectedByMutation` —
    only reads whose scan scope covers the mutated path are dropped
    (prevents the #1475-A stale-read race); unrelated reads keep their win.
  - tree-wide / unknown write sets (`run_command`, `delegate`, ...): all
    pending speculations dropped (#1590-A lineage).
- **Context-fill throttling** mirrors `preExecuteReadOnlyTools` (skip at
  critical fill).
- **Duplicate suppression.** Calls already covered by the cross-turn
  speculator cache or the memo cache are not re-executed; the batch
  pre-executor likewise skips indexes covered by a stream future.
- **Invisible failure.** A failed/aborted speculation is simply re-executed
  by the sequential loop — behavioral correctness is preserved; only latency
  changes.
- **Turn retry.** On stream error the speculator is aborted before collect so
  the retry loop never waits on doomed I/O.

## Telemetry

`debug.Log("parallel", "stream-spec: ...")` covers start, commit and
invalidation events for latency attribution.

## Tests

`internal/agent/spec_stream_test.go`: commit path, colliding-read
invalidation, tree-wide invalidation, unrelated-mutation preservation,
non-safe-tool skip, abort-no-deadlock, and decode-tail overlap timing.
