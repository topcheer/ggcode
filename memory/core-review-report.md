# Core Module Deep Code Review Report

**Scope**: `internal/agent/`, `internal/subagent/`, `internal/swarm/`, `internal/context/`, `internal/session/`, `internal/checkpoint/`
**Reviewer**: agent-core-reviewer
**Date**: 2025-01-27

---

## Summary

Reviewed 31 Go source files (~20k LOC non-test, ~14k LOC test) across 6 packages. The codebase is generally well-structured with thorough error handling, proper mutex usage, and good test coverage. Key concerns include a global mutex for reflection-based tool sync, memory-bounded buffers in session loading, and some potential deadlocks in nested lock acquisition.

---

## Critical Issues

### C-1: `syncToolWorkingDir` uses a global mutex — serialization bottleneck and potential deadlock
**File**: `internal/agent/agent_tool.go:256`
**Severity**: Critical

`syncToolWorkingDir` uses a package-level `var toolWorkingDirMu sync.Mutex` which serializes ALL tool executions across ALL agents. With sub-agents and swarm teammates each having their own agent instance, every tool invocation contends on this single lock.

```go
var toolWorkingDirMu sync.Mutex  // package-level global

func syncToolWorkingDir(t tool.Tool, dir string) {
    toolWorkingDirMu.Lock()         // blocks ALL agents
    defer toolWorkingDirMu.Unlock()
    // reflection to set WorkingDir...
}
```

The comment says "With Registry.Clone(), each agent has independent tool instances, so this mutex should never be contended" — but it is still a global lock. If any tool lacks Clone() or the registry is shared, this becomes a serialization point and a potential deadlock source if the reflection triggers any callback that tries to acquire another lock.

**Recommendation**: Move the mutex to the Agent struct or the tool registry. Since each agent has cloned tools, the agent-level lock is sufficient.

---

### C-2: Nested mutex acquisition in swarm Manager — potential deadlock
**File**: `internal/swarm/manager.go:275-297`
**Severity**: Critical

`CancelAll()` acquires `m.mu` then iterates teams acquiring `team.mu` then `tm.mu`. Meanwhile, `GetTeammateResult()` acquires `m.mu` then calls `team.getTeammate()` which acquires `team.mu.RLock()`. The lock ordering is `m.mu -> team.mu -> tm.mu`.

However, `DeleteTeam()` (line 128) acquires `m.mu`, then `team.mu`. And `emit()` (line 441) acquires `m.mu` inside `teammate_idle` handling, but `emit()` is called FROM the teammate goroutine which may already hold `tm.mu`. If `emit()` tries to acquire `m.mu` while `CancelAll()` holds `m.mu` and waits for `tm.mu`, deadlock occurs.

Actually examining more carefully: `emit()` is called from `runTeammateLoop` (in `idle_runner.go`) which does NOT hold `tm.mu` when calling emit. The idle runner sets status via `tm.setStatus()` (which acquires/releases tm.mu) then calls `m.emit()`. So the actual deadlock risk is lower than initially suspected.

**Revised severity**: High (not Critical). The lock ordering `m.mu -> team.mu -> tm.mu` is consistently followed. But `emit()` acquires `m.mu` and could be called while the caller holds no other locks — this is safe. The remaining risk is if a future change violates this ordering.

---

### C-3: `computeFileChange` reads file content outside the tool execution — TOCTOU race
**File**: `internal/agent/agent_tool.go:189-231`
**Severity**: High

`computeFileChange` reads the file to get `oldContent`, then returns it for diff display and checkpoint saving. Between `computeFileChange` and the actual tool execution, the file could be modified by another process or agent. This means:
1. The diff preview shows stale old content
2. The checkpoint saves stale old content — undo would restore wrong state

```go
func (a *Agent) computeFileChange(tc provider.ToolCallDelta) (...) {
    // reads file here
    data, err := os.ReadFile(filePath)
    oldContent = string(data)
    // ...returns oldContent...
    // ...then executeFileTool calls safeExecute which executes the tool
    // ...the tool may read the file AGAIN and see different content
}
```

**Recommendation**: For `write_file`, pass the oldContent directly to the tool so it uses the same snapshot. For `edit_file`, the tool itself does the replacement — the diff preview may be wrong if the file changed between read and edit.

---

## High Issues

### H-1: `Agent.Provider()` uses write lock (`mu.Lock`) for a read operation
**File**: `internal/agent/agent.go:238-242`
**Severity**: High

```go
func (a *Agent) Provider() provider.Provider {
    a.mu.Lock()        // should be RLock
    defer a.mu.Unlock()
    return a.provider
}
```

This is a write lock for a read-only getter, which unnecessarily blocks all other readers. Compare with `ToolRegistry()` which correctly uses `RLock`.

**Recommendation**: Change to `a.mu.RLock()` / `a.mu.RUnlock()`.

---

### H-2: `Agent.PermissionPolicy()` uses write lock for read operation
**File**: `internal/agent/agent.go:166-170`
**Severity**: High

```go
func (a *Agent) PermissionPolicy() permission.PermissionPolicy {
    a.mu.Lock()        // should be RLock
    defer a.mu.Unlock()
    return a.policy
}
```

Same issue as H-1.

**Recommendation**: Change to `a.mu.RLock()` / `a.mu.RUnlock()`.

---

### H-3: `CheckpointManager()` uses write lock for read operation
**File**: `internal/agent/agent.go:325-329`
**Severity**: High

Same pattern — write lock for a getter. Should use `RLock`.

---

### H-4: `SystemPrompt()` acquires `RLock` but iterates `contextManager.Messages()` without contextManager lock
**File**: `internal/agent/agent.go:252-268`
**Severity**: High

```go
func (a *Agent) SystemPrompt() string {
    a.mu.RLock()
    defer a.mu.RUnlock()
    msgs := a.contextManager.Messages()  // this acquires contextManager.mu internally
    ...
}
```

This works because `contextManager.Messages()` has its own lock. But it means `a.mu` and `contextManager.mu` are acquired in sequence. If any code path acquires them in reverse order, deadlock follows. Currently safe but fragile.

---

### H-5: Swarm `SetWorkingDir` not thread-safe
**File**: `internal/swarm/manager.go:391-393`
**Severity**: High

```go
func (m *Manager) SetWorkingDir(dir string) {
    m.workingDir = dir  // no lock!
}
```

`workingDir` is read in `SpawnTeammate` inside `m.mu`, but written here without any lock. This is a data race.

**Recommendation**: Acquire `m.mu.Lock()` before writing.

---

### H-6: `Team.snapshot()` calls `t.Tasks.List()` which acquires its own mutex — lock nesting
**File**: `internal/swarm/team.go:191-194`
**Severity**: High

```go
func (t *Team) snapshot() TeamSnapshot {
    t.mu.RLock()
    defer t.mu.RUnlock()
    // ...
    if t.Tasks != nil {
        taskCount = len(t.Tasks.List())  // t.Tasks.List() acquires task.Manager.mu
    }
```

This acquires `team.mu.RLock` then `task.Manager.mu.Lock`. If any code path acquires them in reverse, deadlock. Currently safe but should be documented or refactored.

---

### H-7: Session `List()` is O(n^2) — loads every session to check for user interaction
**File**: `internal/session/store.go:455-517`
**Severity**: High

`List()` calls `repairIndex` then iterates all index entries, calling `loadSession()` for each one to check `HasUserInteraction()`. With hundreds of sessions, this loads every JSONL file from disk. The `loadSession()` function does a full scan of each JSONL file.

**Recommendation**: Store `HasUserInteraction` in the index entry, or at minimum skip the full message parsing and just check for user messages in the first pass.

---

### H-8: `appendRecordLine` in session store opens file without checking for directory existence
**File**: `internal/session/store.go:875-894`
**Severity**: Medium-High

If the session directory is deleted externally, `appendRecordLine` will fail with a confusing error. While `NewJSONLStore` creates the directory, long-running processes could encounter this.

---

## Medium Issues

### M-1: `EstimateTokens` treats all non-ASCII as CJK — overestimates for Latin Extended, emoji
**File**: `internal/context/tokenizer.go:6-17`
**Severity**: Medium

```go
for _, r := range text {
    if r > 127 {
        cjk++  // Latin Extended, Cyrillic, emoji all counted as CJK
    } else {
        ascii++
    }
}
```

This overestimates token count for European languages (Latin Extended-A/B, Cyrillic, Greek) which typically use 2-3 chars/token, not 1.5. The function is used for context window management — overestimation is safer than underestimation, but could trigger premature compaction.

---

### M-2: `Agent.contextManager` accessed without holding `a.mu` in many places
**Files**: `internal/agent/agent.go`, `internal/agent/agent_compact.go`
**Severity**: Medium

Many methods access `a.contextManager` directly without holding `a.mu`:
- `RunStreamWithContent` (line 398): `a.contextManager.Add(...)`
- `AddMessage` (line 196): `a.contextManager.Add(msg)`
- `Clear` (line 375): `a.contextManager.Clear()`
- `maybeAutoCompact`: `a.contextManager.TokenCount()`

This works because `SetContextManager` is typically called once at init and the contextManager field is effectively immutable after setup. But if called concurrently with `SetContextManager`, there's a data race.

**Recommendation**: Document that `SetContextManager` must not be called concurrently with agent loop execution.

---

### M-3: Sub-agent `Runner.Run()` captures agent output via `strings.Builder` — unbounded memory
**File**: `internal/subagent/runner.go:186-205`
**Severity**: Medium

```go
func (r *Runner) Run(ctx context.Context, ...) (string, error) {
    var output strings.Builder
    // ... accumulates ALL streamed text into output
    // If the agent produces megabytes of output, this grows unbounded
}
```

For long-running sub-agents, the output accumulation can grow very large. There's no truncation or limit.

**Recommendation**: Add a configurable max output size, truncating with a marker if exceeded.

---

### M-4: `Agent.executeFileTool` runs pre-hooks twice for file tools
**File**: `internal/agent/agent_tool.go:90-98, 159-163`
**Severity**: Medium

`executeTool` calls `hooks.RunPreHooks` (line 90), then for file tools it calls `executeFileTool` which calls `hooks.RunPreHooks` AGAIN (line 163). The first call in `executeTool` is skipped because file tools are routed to `executeFileTool` at line 97, but the `env` object passed to `executeFileTool` is from the same `tc.Arguments`. So pre-hooks run once for file tools. Actually reviewing more carefully: `executeTool` checks `if tc.Name == "edit_file" || tc.Name == "write_file"` and immediately returns `a.executeFileTool(...)`, so pre-hooks in `executeTool` are NOT called for file tools. The pre-hooks in `executeFileTool` ARE called. This is correct — no double execution.

**Revised**: Not an issue. Pre-hooks correctly run once.

---

### M-5: Swarm teammate ID generation uses `m.nextTeamID` for both teams and teammates
**File**: `internal/swarm/manager.go:106-108, 184-186`
**Severity**: Medium

```go
// In CreateTeam:
m.nextTeamID++
id := fmt.Sprintf("team-%d", m.nextTeamID)

// In SpawnTeammate:
m.nextTeamID++
tmID := fmt.Sprintf("tm-%d", m.nextTeamID)
```

Both teams and teammates share `nextTeamID`. If 1 team is created (team-1) and then a teammate is spawned (tm-2), then another team (team-3), the IDs are non-sequential for each type but unique overall. The prefix ("team-" vs "tm-") prevents collisions, but the shared counter is confusing.

**Recommendation**: Use separate counters (`nextTeamID` and `nextTeammateID`).

---

### M-6: `indexOf` uses O(n*m) naive string search
**File**: `internal/agent/agent_tool.go:243-250`
**Severity**: Medium

```go
func indexOf(s, substr string) int {
    for i := 0; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
```

This is O(n*m) where n=len(s) and m=len(substr). For large files and search strings, this is slow. Go's `strings.Index` uses optimized algorithms (Rabin-Karp or similar).

**Recommendation**: Replace with `strings.Index(s, substr)`.

---

### M-7: Checkpoint `Revert` only restores first file if multiple checkpoints exist for same file
**File**: `internal/checkpoint/checkpoint.go:83-107`
**Severity**: Medium

`Revert(id)` writes `OldContent` back to the file and truncates the checkpoint list to `[:idx]`. But if later checkpoints modified the same file, only the target checkpoint's OldContent is written. The file state after revert may be inconsistent — it reverts one edit but doesn't account for intermediate edits to the same file.

**Recommendation**: For the common case of sequential edits to the same file, Revert should apply OldContent from the target checkpoint, which is correct. But document that Revert assumes linear file editing history.

---

### M-8: Session `CleanupOlderThan` holds lock per delete operation
**File**: `internal/session/store.go:650-665`
**Severity**: Medium

`CleanupOlderThan` calls `List()` (acquires/releases lock) then iterates calling `Delete()` (each acquires/releases lock). This is correct but for bulk cleanup, holding the lock once would be more efficient.

---

### M-9: Context Manager `Add` does auto-compact synchronously in the add path
**File**: `internal/context/manager.go:130-175`
**Severity**: Medium

The `Add` method checks usage ratio and may trigger `autoSummarize` synchronously within the mutex lock. This means the lock is held during an LLM API call (summarization), blocking all other reads/writes to the context manager.

**Recommendation**: The agent layer handles this via pre-compaction (background), but the context manager itself doesn't guard against this. The `autoSummarize` call inside `Add` should at minimum be documented as potentially blocking.

---

## Low Issues

### L-1: `isJSON` allocates `interface{}` for validation
**File**: `internal/agent/agent.go:836-839`
**Severity**: Low

```go
func isJSON(data json.RawMessage) bool {
    var v interface{}
    return json.Unmarshal(data, &v) == nil
}
```

`json.Unmarshal` into `interface{}` allocates. For a validation check, `json.Unmarshal(data, &struct{}{})` or `json.Valid(data)` would be cheaper.

---

### L-2: `Session.ID` format uses timestamp + random — non-unique in rare cases
**File**: `internal/session/store.go:804-810`
**Severity**: Low

```go
func generateID() string {
    b := make([]byte, 8)
    if _, err := rand.Read(b); err != nil {
        return fmt.Sprintf("%s-%d", ...) // fallback without randomness
    }
    return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b))
}
```

The fallback path (when `rand.Read` fails) uses only timestamp + nanoseconds. Two sessions created in the same nanosecond would collide. Extremely unlikely but possible.

---

### L-3: `checkpoint.generateID` ignores error from `rand.Read`
**File**: `internal/checkpoint/checkpoint.go:137-140`
**Severity**: Low

```go
func generateID() string {
    b := make([]byte, 8)
    _, _ = rand.Read(b)  // error ignored
    return hex.EncodeToString(b)
}
```

If `rand.Read` fails, the ID is all zeros. Collision risk.

---

### L-4: Swarm `Event` struct uses string type for `Type` field
**File**: `internal/swarm/team.go:224-232`
**Severity**: Low

Event types are free-form strings ("teammate_spawned", "teammate_working", etc.). A typo in event type would silently break event handling. Consider using constants or an enum type.

---

### L-5: Sub-agent status is a string type without type safety
**File**: `internal/subagent/manager.go:24-27`
**Severity**: Low

Status constants are plain strings. A typed string (like `task.TaskStatus`) would prevent invalid status values.

---

### L-6: `normalizeProjectMemoryPath` does filepath operations without locking workingDir
**File**: `internal/agent/agent_memory.go`
**Severity**: Low

`normalizeProjectMemoryPath` reads `a.workingDir` parameter (passed from `SetProjectMemoryFiles`), not directly from the struct. This is safe since the caller passes the value. No issue.

---

### L-7: `Teammate.appendEvent` slices to drop oldest — O(n) per append when full
**File**: `internal/swarm/team.go:108-116`
**Severity**: Low

```go
if len(t.events) >= maxTeammateEvents {
    t.events = t.events[1:]  // O(n) copy
    t.eventsDropped++
}
```

When the event buffer is full, every append copies the entire slice. A ring buffer would be more efficient, but with `maxTeammateEvents=200` this is negligible.

---

## Security Observations

### S-1: Checkpoint `OldContent` stored in memory — sensitive data exposure
**File**: `internal/checkpoint/checkpoint.go`

The full file contents before edits are stored in memory. For files containing secrets, API keys, or credentials, these remain in memory until the checkpoint is evicted or the manager is garbage collected. Consider:
1. Limiting max content size per checkpoint
2. Not checkpointing files that match `.gitignore` patterns for secrets

### S-2: Session JSONL files contain full conversation history including tool inputs
**File**: `internal/session/store.go`

Tool call arguments (which may contain file contents, API keys in env vars) are persisted in JSONL. Files are created with 0600 permissions, which is appropriate. No issue with permissions, but users should be aware that session files contain sensitive data.

---

## Performance Observations

### P-1: Context Manager `Messages()` returns a copy every call
**File**: `internal/context/manager.go`

Every call to `Messages()` creates a new slice copy. In the agent loop, this is called every iteration. For long conversations, this is an O(n) allocation per iteration.

### P-2: `Session.loadSession` allocates `lightweightEntry` slices for large sessions
**File**: `internal/session/store.go:373-409`

The single-pass load accumulates all post-checkpoint entries in a slice. For sessions with thousands of messages after a checkpoint, this allocates a large slice.

### P-3: Sub-agent `Manager.Each` holds `m.mu` during callback execution
**File**: `internal/subagent/manager.go:547-560`

```go
func (m *Manager) Each(fn func(*SubAgent)) {
    m.mu.Lock()
    // ...iterates and calls fn while holding m.mu
}
```

The callback runs under the manager lock. If the callback blocks or does I/O, it blocks all manager operations.

---

## Test Coverage Assessment

### Good Coverage
- `internal/agent/`: `agent_test.go` (1644 lines), `agent_coverage_test.go` (997 lines) — thorough coverage of core loop, tool execution, permission, compact, autopilot, and edge cases
- `internal/session/`: `store_test.go` (638 lines) — covers Save/Load/List/Delete/Append/Checkpoint/Empty session handling
- `internal/checkpoint/`: `checkpoint_test.go` — covers Save/Undo/Revert/MaxCheckpoints/Clear
- `internal/swarm/`: Multiple test files covering lifecycle, race conditions, E2E, team, and idle runner
- `internal/context/`: `manager_test.go` (842 lines), `tokenizer_test.go` — thorough coverage
- `internal/subagent/`: `manager_test.go`, `event_test.go`, `coverage_test.go`

### Missing/Weak Coverage
1. **Concurrent session writes**: No test for two goroutines calling `Save` or `AppendMessage` simultaneously on the same session
2. **Agent loop with concurrent `SetProvider`/`SetWorkingDir`**: No test for the agent loop running while configuration is changed
3. **Swarm teammate crash recovery**: No test for what happens when a teammate goroutine panics (covered by `safego` but not tested)
4. **Context manager concurrent `Add` + `Summarize`**: No test for concurrent add and summarize operations
5. **Checkpoint undo for binary files**: Checkpoint stores `string` content — no test for binary file handling
6. **Integration tests gated behind `integration_local` tag**: Many integration tests are not runnable in CI without API keys

---

## Architecture Observations

### Positive
1. **Clean interface boundaries**: `ContextManager` interface allows different implementations. `AgentRunner` interface in swarm decouples from concrete agent.
2. **Defensive programming**: `safeExecute` with panic recovery, `fillCancelledToolResults` for protocol compliance, `shouldIgnoreAutoCompactError` for transient failures.
3. **Good separation of concerns**: Agent split into focused files (core, autopilot, compact, memory, tool, precompact).
4. **Atomic file writes**: Session store uses tmp+rename pattern. Checkpoint uses `util.AtomicWriteFile`.
5. **Proper context cancellation**: Agent loop checks `ctx.Err()` at multiple points, including mid-tool-execution.

### Concerns
1. **Tight coupling between Agent and ContextManager**: Agent directly calls many contextManager methods without an abstraction layer. This makes testing the agent in isolation harder.
2. **Swarm teammate lifecycle is complex**: The interplay between `team.mu`, `tm.mu`, and `m.mu` requires careful analysis. A state machine diagram would help.
3. **Session store mutex is coarse**: A single `sync.Mutex` for all operations. For high-throughput scenarios (many concurrent sessions), this could be a bottleneck.

---

## Action Items (Priority Order)

| Priority | ID | Summary | Effort |
|----------|-----|---------|--------|
| P0 | C-1 | Move `toolWorkingDirMu` to Agent or Registry level | Small |
| P0 | H-1,H-2,H-3 | Fix write-lock-for-read in `Provider()`, `PermissionPolicy()`, `CheckpointManager()` | Trivial |
| P0 | H-5 | Add mutex to `SetWorkingDir` in swarm Manager | Trivial |
| P1 | C-3 | Address TOCTOU in `computeFileChange` | Medium |
| P1 | H-7 | Optimize session `List()` to avoid loading all sessions | Medium |
| P1 | M-6 | Replace naive `indexOf` with `strings.Index` | Trivial |
| P2 | M-3 | Add output size limit to sub-agent runner | Small |
| P2 | M-5 | Separate team/teammate ID counters | Trivial |
| P2 | M-9 | Document blocking behavior of context manager auto-compact | Trivial |
| P3 | L-1,L-3 | Minor optimizations in `isJSON` and `generateID` | Trivial |
| P3 | L-4,L-5 | Add type safety for event/status strings | Small |
