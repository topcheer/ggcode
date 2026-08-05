# Stream Stall Detection

## Problem

When an LLM provider stalls during streaming (network degradation, server
overload, or dropped connection), the user sees a frozen spinner with no
feedback. They cannot distinguish "model is thinking" from "connection died."

## Solution

`internal/agent/stream_health.go` wraps the provider stream channel with a
timer that injects an advisory `StreamEventSystem` event when no data arrives
for `streamStallThreshold` (30s).

The warning flows through the existing `StreamEventSystem` handling in
`streamChatResponse`, which forwards it to `onEvent`. The TUI already displays
`StreamEventSystem` events as system notification messages in the chat panel
(`internal/tui/submit.go:354-363`), so no UI changes are needed.

## Design Decisions

1. **Advisory only** -- does not cancel or interrupt the stream. The user
   retains control via Ctrl+C.
2. **Single warning per stall period** -- resets when a real event arrives,
   preventing spam.
3. **30s threshold** -- generous enough for extended reasoning pauses
   (Anthropic can have 10-20s gaps between thinking chunks) but catches real
   network degradation.
4. **Zero LLM cost** -- purely deterministic timer-based detection.
5. **Thread-safe** -- all events (including stall warnings) flow through one
   channel, avoiding concurrent `onEvent` calls.
6. **Backpressure-safe** -- if the consumer is slow, the goroutine blocks on
   `out <- event` and the timer cannot fire. A slow consumer is not a stream
   stall, so suppressing the warning is correct behavior.

## Files

- `internal/agent/stream_health.go` -- wrapper function + threshold constant
- `internal/agent/stream_health_test.go` -- 5 tests
- `internal/agent/agent.go` -- wired into `streamChatResponse`

## Competitor Comparison

| Product       | Stall Detection |
|---------------|----------------|
| Claude Code   | Shows "still working" after delay |
| Cursor        | Progress bar with elapsed time |
| Aider         | No stall feedback |
| Cline         | VS Code progress notification |
| ggcode        | Advisory stall warning via StreamEventSystem |
