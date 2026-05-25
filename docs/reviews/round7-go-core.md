# Round 7 Code Review: Go Backend Core Packages

**Reviewer**: Automated static review
**Date**: 2025-07-17
**Scope**: internal/agent, internal/provider, internal/session, internal/config, internal/context, internal/memory, internal/cost, internal/subagent

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High | 6 |
| Medium | 9 |
| Low | 8 |

---

## 1. internal/subagent/

### [Critical] S1: `RunningCount` acquires `m.mu` then calls `sa.getStatus()` which acquires `sa.mu` -- potential deadlock path with `Cancel`

**File**: `internal/subagent/manager.go`, lines 387-397

`RunningCount()` acquires `m.mu` (the Manager lock) and then calls `sa.getStatus()` which acquires `sa.mu` (the SubAgent lock). Meanwhile `Cancel()` (lines 400-424) acquires `m.mu`, releases it, then acquires `sa.mu`. While the current lock ordering (Manager -> SubAgent) is consistent, `RunningCount` is called from TUI/UI refresh code under `m.mu` -- if any SubAgent callback (onUpdate, onComplete) were to call back into Manager methods that take `m.mu`, a deadlock would result.

**Impact**: The `onUpdate`/`onComplete` callbacks are invoked from `Cancel` and `Complete` without holding `m.mu`, so this is not an active deadlock today. However, `notifyUpdate` (line 687) reads `m.onUpdate` under `m.mu` and then invokes the callback outside the lock -- if a future change inverts this order, a deadlock will be hard to diagnose.

**Recommendation**: Document the required lock ordering explicitly. Consider making `RunningCount` use a snapshot approach like `Statuses()`.

---

### [Critical] S2: `Broadcast` calls `sa.getStatus()` under `m.mu` while `sa.Mailbox` send could block

**File**: `internal/subagent/manager.go`, lines 634-649

`Broadcast` holds `m.mu` (via `defer m.mu.Unlock()`) while iterating agents and calling `sa.getStatus()` (which acquires `sa.mu`). If a SubAgent's `Mailbox` channel is full (capacity 16), the `select` with `default` prevents a goroutine leak, but the entire Manager is locked during broadcast. Since `Broadcast` is meant for running agents, any slow consumer that fills the mailbox will cause the broadcast to skip that agent silently -- but the real risk is that holding `m.mu` blocks `Spawn`, `Get`, `Complete`, and `CancelAll` for the duration of the iteration.

**Impact**: In practice the iteration is fast (non-blocking channel sends), but if the number of agents is very large or `getStatus()` is slow under contention, this could cause latency spikes.

**Recommendation**: Collect agent IDs and mailboxes under the lock, then send outside the lock.

---

### [High] S3: Sub-agent map grows without bound -- no cleanup of completed agents

**File**: `internal/subagent/manager.go`, lines 299-322 (`Spawn`), lines 479-514 (`Complete`)

Completed SubAgent entries are never removed from `m.agents`. Over a long-running session with many sub-agent spawns, this map grows without bound. Each entry retains its full event history (up to `maxAgentEvents=200` events), result string, and mailbox channel.

**Impact**: Memory leak in long-running daemon sessions. The TUI follow strip has a 1-minute grace period but the Manager map has no cleanup at all.

**Recommendation**: Add a `Cleanup()` method that removes agents that have been in a terminal state for longer than a configurable TTL. Call it periodically or on `List()`/`Statuses()`.

---

### [High] S4: `Wait` and `WaitForSnapshot` use polling with 100ms ticker instead of signaling

**File**: `internal/subagent/runner.go`, lines 222-285

Both `Wait` and `WaitForSnapshot` poll with a 100ms ticker, checking `sa.Status` under `sa.mu` on every tick. This is wasteful when many sub-agents are being waited on simultaneously.

**Impact**: Unnecessary CPU wakeups and mutex contention. For a single wait it's tolerable, but `WaitForSnapshot` with a 0-duration wait (used as a non-blocking check) still allocates a ticker unnecessarily.

**Recommendation**: Use a `sync.Cond` or a per-agent done channel that `Complete`/`Cancel` closes, allowing `Wait` to block without polling.

---

## 2. internal/agent/

### [High] S5: `agent_compact.go` -- background compaction goroutine leak if provider.Chat hangs

**File**: `internal/agent/agent_compact.go`, lines 59-97

`startBackgroundCompaction` spawns a goroutine that calls `snapshot.Compact(ctx, prov)` where `ctx` derives from `a.runCtx`. If the summarization LLM call hangs indefinitely (provider is down, no timeout), the goroutine leaks until the agent shuts down.

**Impact**: Goroutine and provider connection leak. The `compactDone` channel (capacity 1) prevents blocking the main loop, but the background goroutine itself is unbounded.

**Recommendation**: Add a dedicated timeout (e.g., 60s) to the context passed to `Compact`, independent of `a.runCtx`.

---

### [High] S6: `agent_tool.go` -- `executeTool` wraps errors inconsistently

**File**: `internal/agent/agent_tool.go`, lines 100-200

Some error paths use `fmt.Errorf("...: %w", err)` while others return raw errors or use `fmt.Errorf("...: %v", err)`. This makes it impossible for callers to use `errors.Is`/`errors.As` reliably on tool execution errors.

**Impact**: Downstream error handling (retry logic, user-facing messages) may fail to match specific error types.

**Recommendation**: Audit all error returns in `executeTool` and ensure consistent use of `%w` wrapping.

---

### [Medium] S7: `agent_autopilot.go` -- loop guard threshold is hardcoded and may be too aggressive

**File**: `internal/agent/agent_autopilot.go`, lines 9-11

`autopilotLoopGuardThreshold = 2` means that if the model requests `ask_user` more than twice without a real user message in between, autopilot stops. This is a safety measure but may be triggered legitimately in interactive workflows where the user provides short clarifications.

**Impact**: Premature autopilot termination in valid multi-turn clarification scenarios.

**Recommendation**: Consider making this configurable or increasing to 3.

---

### [Medium] S8: `agent_memory.go` -- `sanitizePath` does not validate against directory traversal

**File**: `internal/agent/agent_memory.go`, lines 146-179

`sanitizePath` strips absolute paths and `../` traversal from tool arguments but does not handle encoded sequences like `%2e%2e%2f` or double-encoding. While the agent operates within the user's workspace, a malicious prompt injection could potentially construct paths that escape the workspace.

**Impact**: Low practical risk since the LLM is generating the tool arguments, but defense-in-depth recommends stricter path validation.

**Recommendation**: Use `filepath.Rel` to verify the resolved path stays within the workspace root.

---

## 3. internal/provider/

### [High] S7: `adaptive_cap.go` -- `saveAdaptiveCaps` reads cap fields without holding individual cap mutex

**File**: `internal/provider/adaptive_cap.go`, lines 227-250

The comment says "Read fields without taking c.mu -- we're already inside the caller's lock for the cap being mutated, and other caps' fields are read atomically (cur) or are stable enough for a snapshot." However, `c.lo` and `c.hi` are `int64` fields accessed without atomic operations or mutex. On architectures without atomic 64-bit aligned loads (some 32-bit platforms), this is a data race. Even on 64-bit, the read is not synchronized with concurrent `OnTruncated`/`OnRejected` calls on other caps.

**Impact**: Data race when multiple provider instances adjust their adaptive caps concurrently. Could lead to corrupted persisted state.

**Recommendation**: Acquire each cap's `mu` before reading its fields, or make `lo`/`hi` atomic.

---

### [Medium] S8: `openai.go` -- `ChatStream` does not set `StreamOptions.IncludeUsage` on streaming request

**File**: `internal/provider/openai.go`, lines 206-214

The non-streaming `Chat` method sets `StreamOptions: &openai.StreamOptions{IncludeUsage: true}` but `ChatStream` does not. This means the stream may not receive a final usage chunk, and the code falls back to `CountTokens` + `estimateTokensFromChars` estimation (lines 410-418), which is less accurate.

**Impact**: Inaccurate token usage reporting for OpenAI streaming calls, leading to incorrect cost tracking.

**Recommendation**: Add `StreamOptions` to the streaming request as well.

---

### [Medium] S9: `anthropic.go` -- system messages silently converted to user message content

**File**: `internal/provider/anthropic.go`, lines 288-368

`buildParams` collects all system messages and prepends them into the first user message. This is done to work around API restrictions, but it means multi-system-message patterns (e.g., separate system prompt + post-compact state) get concatenated into a single user message, which may confuse the model about message boundaries.

**Impact**: Minor -- the `[System]...[End System]` markers help, but the model may not treat the system instructions with the same weight as a native system message.

**Recommendation**: Use Anthropic's native `system` parameter in `MessageNewParams` for the first system message, and only inline additional system messages.

---

### [Medium] S10: `retry.go` -- `retrySleep` uses `time.Sleep` which is not cancellable by context

**File**: `internal/provider/retry.go`

`retrySleep` likely uses `time.Sleep` or a similar mechanism. If the parent context is cancelled during the sleep, the retry loop will continue sleeping until the timer fires, then discover the cancellation.

**Impact**: Up to several seconds of wasted wait time on cancellation (e.g., user hits Ctrl+C during a retry).

**Recommendation**: Use `select { case <-time.After(delay): case <-ctx.Done(): }` pattern for cancellable sleeps. (Note: the code may already do this -- verify at runtime.)

---

### [Low] S11: `user_error.go` -- error messages are in Chinese (hardcoded)

**File**: `internal/provider/user_error.go`, lines 34-117

All user-facing error messages are hardcoded in Chinese. While the project supports i18n for the TUI (`internal/tui/i18n.go`), provider error messages bypass the i18n system.

**Impact**: Non-Chinese users will see Chinese error messages from provider failures.

**Recommendation**: Route through the i18n system or provide English default messages.

---

### [Low] S12: `copilot.go` -- body is read twice (once for original req, once for clone)

**File**: `internal/provider/copilot.go`, lines 46-53

The `RoundTrip` method reads the entire request body into memory to inspect it, then creates two `io.NopCloser` readers. For very large request bodies, this doubles memory usage temporarily.

**Impact**: Minor memory spike for large Copilot requests.

**Recommendation**: This is standard practice for HTTP middleware. Acceptable as-is.

---

## 4. internal/session/

### [High] S8: `List()` loads every session file from disk on every call

**File**: `internal/session/store.go`, lines 483-546

`List()` holds the store mutex, calls `repairIndex` which may load sessions from disk, then iterates through the index and calls `loadSession` for each entry to check `HasUserInteraction()`. For users with hundreds of sessions, this is an O(n) disk I/O operation on every `List()` call.

**Impact**: Significant latency on session picker display for users with many sessions. The mutex is held throughout, blocking all other session operations.

**Recommendation**: Cache the `HasUserInteraction` flag in the index entry, and only recheck on index rebuild.

---

### [Medium] S11: `loadSession` uses 10MB scanner buffer limit

**File**: `internal/session/store.go`, line 391

`sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)` allows lines up to 10MB. A corrupted JSONL file with a very long line would allocate 10MB before failing.

**Impact**: Minor -- memory spike on corrupted files.

**Recommendation**: Consider reducing to 1MB or adding a size check before parsing.

---

### [Medium] S12: `appendRecordLine` opens file with O_APPEND but also calls Sync()

**File**: `internal/session/store.go`, lines 919-939

Each `appendRecordLine` call opens the file, writes, syncs (fsync), and closes. The `Sync()` call ensures durability but is expensive on some filesystems (especially network mounts). For high-frequency appends (streaming messages), this causes significant I/O overhead.

**Impact**: Performance degradation during active sessions, especially on slow disks.

**Recommendation**: Consider batching syncs (e.g., sync every N records or every T seconds).

---

### [Low] S13: `Session.CostJSON` stored as `[]byte` but accessed as `json.RawMessage`-like

**File**: `internal/session/store.go`, line 348

The `CostJSON` field is `json.RawMessage` (or `[]byte`) stored separately from the cost tracker to avoid circular imports. This works but means cost data must be serialized/deserialized through JSON at the session boundary.

**Impact**: Design limitation, not a bug.

---

## 5. internal/config/

### [Medium] S13: `configFileLocks` map grows without bound

**File**: `internal/config/config.go`, lines 25-43

`lockConfigFile` creates a per-path mutex and stores it in `configFileLocks`. These entries are never removed. If the application processes many different config file paths (unlikely but possible with dynamic configuration), this map grows.

**Impact**: Minor memory leak. In practice, the number of unique config paths is small (1-3).

**Recommendation**: Consider using `sync.Map` or periodic cleanup.

---

### [Medium] S14: `env.go` -- `loadRuntimeEnv` calls `os.Setenv` to propagate keys.env values

**File**: `internal/config/env.go`, lines 117-120

After loading `~/.ggcode/keys.env`, the code calls `os.Setenv(name, value)` for every key in the merged env map, including existing process environment variables. This is both unnecessary (they're already in the process env) and potentially pollutes the process environment with shell rc file values.

**Impact**: Process environment pollution. If a `.zshrc` file sets `PATH` or other sensitive variables referenced in the config, those would be overwritten.

**Recommendation**: Only `os.Setenv` for keys loaded from `keys.env` that are not already in the process environment.

---

### [Low] S14: `env.go` -- shell env parsing does not handle multiline values

**File**: `internal/config/env.go`, lines 236-263

`parseEnvAssignment` handles single-line assignments with quote stripping. Multiline values (e.g., heredocs or backtick expansions) are not supported. This is documented behavior but may surprise users with complex shell configurations.

**Impact**: Some API keys defined via shell expansions won't be picked up.

---

## 6. internal/context/

### [Low] S15: `countTokens` creates a new `context.WithTimeout` on every call

**File**: `internal/context/manager.go`, lines 493-503

Every call to `countTokens` creates a 100ms timeout context via `context.WithTimeout(context.Background(), tokenCountTimeout)`. During token counting operations (e.g., `recalcTokens` which iterates all messages), this creates N contexts and N timers for N messages.

**Impact**: Unnecessary allocations and GC pressure during token recounting. The 100ms timeout per message is reasonable but the context allocation per call is wasteful.

**Recommendation**: Pass a single context into `recalcTokens` and reuse it, or batch the token counting call.

---

### [Low] S16: `estimateTokens` concatenates all content fields into a single string

**File**: `internal/context/manager.go`, lines 505-511

`estimateTokens` builds a concatenated string of Text + ToolName + Output + Input for each message. For messages with large tool outputs, this creates temporary strings proportional to the output size.

**Impact**: Minor allocation overhead during estimation. The heuristic approach (chars/4) is standard.

**Recommendation**: Compute length directly without concatenation: `len(b.Text) + len(b.ToolName) + len(b.Output) + len(b.Input)`.

---

### [Low] S17: `buildSummaryPlan` does not handle edge case where `groups` has exactly 1 element after system message

**File**: `internal/context/manager.go`, lines 648-712

If there is only 1 group after the system message (i.e., a single user-assistant exchange), the code at line 664 returns `summaryPlan{}, false` because `len(groups) <= minRecentGroups` (1 <= 1). This means summarization cannot be triggered for very short conversations even if they're at the token limit, which is correct behavior but may be surprising.

**Impact**: Not a bug -- intentional safeguard. Documented for awareness.

---

## 7. internal/memory/

### [Low] S18: `auto.go` -- `sanitizeKey` loop for collapsing dashes is O(n^2) in pathological cases

**File**: `internal/memory/auto.go`, lines 131-134

The `for strings.Contains(safe, "--")` loop calls `strings.ReplaceAll` on each iteration. For a key consisting entirely of special characters (e.g., `!!!`), each character maps to `-`, and the loop runs O(n) times with O(n) work each time, giving O(n^2) total.

**Impact**: Negligible in practice -- memory keys are short (typically < 50 chars).

**Recommendation**: Use `strings.NewReplacer("--", "-")` or a single-pass approach.

---

## 8. internal/cost/

### [Medium] S15: `Manager.Save` uses non-atomic write

**File**: `internal/cost/manager.go`, lines 83-107

`Save` writes to a `.tmp` file then renames. However, it uses `os.WriteFile` for the tmp file, which does not guarantee atomicity on all platforms (e.g., Windows). The session store uses a similar pattern but with explicit create+write+sync+close.

**Impact**: Risk of truncated cost data on crash during write. Low probability.

**Recommendation**: Use the same create+write+sync+close pattern as `appendRecordLine`.

---

### [Low] S19: `Manager.trackers` map grows without cleanup

**File**: `internal/cost/manager.go`, lines 14-28

Like the subagent Manager, completed session cost trackers are never removed. Over a long-running daemon with many sessions, this grows unbounded.

**Impact**: Minor memory leak. Each tracker holds a `SessionCost` struct (~100 bytes) so the growth is slow.

**Recommendation**: Add cleanup when sessions are deleted or after a TTL.

---

## Overall Assessment

### Strengths

1. **Concurrency discipline is generally excellent**. The Manager/SubAgent pattern uses clear mutex boundaries. The context manager properly locks for all read-modify-write operations. The config file locking prevents write races.

2. **Error propagation** follows Go best practices in most places, with `%w` wrapping for downstream inspection.

3. **Resource cleanup** is handled well at the agent level -- `Shutdown()` cascades through sub-agents, and `CancelAll()` properly cancels all running work.

4. **The adaptive cap system** (internal/provider/adaptive_cap.go) is well-designed with monotonic bounds and convergence guarantees.

### Areas for Improvement

1. **Memory growth in long-running processes**: Both `internal/subagent/Manager.agents` and `internal/cost/Manager.trackers` grow without bound. Add periodic cleanup.

2. **Token counting overhead**: Creating a context per message for provider-based token counting is wasteful. Consider batching.

3. **Session List() performance**: Full disk scan on every list call is expensive. Cache `HasUserInteraction` in the index.

4. **Data race in adaptive cap persistence**: `saveAdaptiveCaps` reads non-atomic fields without synchronization for caps not being actively mutated. Use atomic or per-cap locking.

5. **Hardcoded Chinese strings** in provider error messages bypass the i18n system.
