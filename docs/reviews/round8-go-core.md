# Round 8 Go Core Review

**Reviewer:** go-core (teammate)
**Date:** 2025-07-12
**Scope:** `internal/agent/`, `internal/provider/`, `internal/tool/`, `internal/session/`, `internal/metrics/`, `internal/context/`, `internal/permission/`, `internal/subagent/`, `internal/swarm/`, `internal/knight/`

---

## Executive Summary

Reviewed 10 packages covering the agent loop, LLM providers, tool execution, session persistence, metrics collection, context window management, permissions, sub-agent coordination, swarm/team management, and the Knight background agent. The codebase is well-structured with consistent use of mutexes for shared state and proper context cancellation propagation. Issues found range from a goroutine lifecycle gap in the metrics collector to several medium-severity concurrency and resource management concerns.

**Totals:** 0 Critical, 4 High, 11 Medium, 10 Low

---

## Findings by Package

### `internal/metrics/` (collector.go)

#### [H-01] High: Goroutine leak if `Stop()` is never called on `Collector`

**File:** `internal/metrics/collector.go`, lines 17-55

The `NewCollector()` constructor launches a goroutine via `safego.Go()` that loops forever reading from `ch` and `stopCh`. If `Stop()` is never called (e.g., on error paths, early return, or when the agent is interrupted before clean shutdown), the goroutine leaks.

The drain path itself is correctly implemented using a non-blocking `select` with `default: return`:

```go
func NewCollector(capacity int, persist func(MetricEvent)) *Collector {
    c := &Collector{
        ch:     make(chan MetricEvent, capacity),
        stopCh: make(chan struct{}),
    }
    safego.Go("metrics.collector", func() {
        for {
            select {
            case m := <-c.ch:
                persist(m)
            case <-c.stopCh:
                // Drain remaining events (non-blocking)
                for {
                    select {
                    case m := <-c.ch:
                        persist(m)
                    default:
                        return
                    }
                }
            }
        }
    })
    return c
}
```

When `Stop()` is called, `stopCh` is closed, the drain loop correctly exits after consuming buffered events, and the goroutine terminates. The drain is non-blocking (uses `default` branch, not `range`), so there is no deadlock.

However, if `Stop()` is never called, the goroutine runs indefinitely. There is no context cancellation or final cleanup mechanism.

**Impact:** Goroutine leak in error/early-return paths that skip `Stop()`. In long-running daemon/knight sessions, this is one leaked goroutine per Collector instance.

**Recommendation:** Add a `finalizer` or ensure all code paths call `Stop()`. Consider accepting a `context.Context` in `NewCollector` so the goroutine exits when the context is cancelled even without calling `Stop()`.

---

### `internal/subagent/` (manager.go)

#### [H-02] High: Potential goroutine leak on context cancellation in `Run()`

**File:** `internal/subagent/manager.go`, lines ~230-290

The `Run()` method spawns a goroutine to execute the sub-agent task, writes results to a channel, and selects on that channel plus context cancellation. When the context is cancelled, the select takes the `<-ctx.Done()` branch and returns, but the spawned goroutine (`go func() { ... ch <- result }()`) continues running with no way to cancel it.

```go
func (m *Manager) Run(ctx context.Context, ...) (string, error) {
    // ...
    ch := make(chan runResult, 1)
    go func() {
        // This goroutine is not cancelled when ctx is cancelled
        output := m.runner.Run(taskCtx, ...) // taskCtx is derived from ctx
        ch <- runResult{output: output, ...}
    }()

    select {
    case res := <-ch:
        // ...
    case <-ctx.Done():
        // Returns, but goroutine above still runs
        m.updateStatus(sa.id, StatusCancelled)
        return "", ctx.Err()
    }
}
```

The inner `taskCtx` IS derived from `ctx`, so the runner should respect cancellation eventually. However, the `ch <- runResult` send could block if the buffer is full (buffer is 1, so this is fine), but if the task completes after context cancellation, the send succeeds into the buffered channel and the goroutine exits cleanly. On closer inspection, the buffer size of 1 prevents a permanent leak.

**Revised assessment:** The buffer-1 channel prevents the worst case, but there is still a window where the goroutine runs after cancellation until the runner checks `taskCtx`. This is acceptable behavior but should be documented.

**Impact:** Sub-agent goroutines may run briefly after cancellation. Not a permanent leak but a resource concern under heavy cancellation.

**Recommendation:** Add a comment documenting the intentional design. Consider adding a `sync.WaitGroup` or explicit tracking for clean shutdown observability.

---

#### [H-03] High: `CancelAll()` does not wait for goroutines to finish

**File:** `internal/subagent/manager.go`, lines ~460-480

`CancelAll()` cancels all running sub-agents but returns immediately without waiting for their goroutines to complete. This means:
1. Sub-agent goroutines may still be writing to shared state after `CancelAll()` returns.
2. Tests that call `CancelAll()` and then inspect state may see transient inconsistencies.

```go
func (m *Manager) CancelAll() {
    m.mu.Lock()
    defer m.mu.Unlock()
    for _, sa := range m.agents {
        if sa.cancel != nil {
            sa.cancel()
        }
    }
}
```

**Impact:** Race conditions in tests and shutdown paths that assume `CancelAll()` is synchronous.

**Recommendation:** Add a `WaitGroup` to track running goroutines and `Wait()` in `CancelAll()`, or document that callers must not assume synchronous cancellation.

---

### `internal/swarm/` (manager.go, team.go, idle_runner.go)

#### [H-04] High: `Team.runTask()` goroutine leak on team deletion during task execution

**File:** `internal/swarm/team.go`, lines ~170-230

When a task is running in a goroutine and `team.Delete()` / `team_delete` is called, the teammate's context is cancelled. However, the `runTask` goroutine writes results to `t.resultCh`:

```go
func (t *Team) runTask(ctx context.Context, ...) {
    // ...
    go func() {
        output := runTeammateTask(...)
        select {
        case t.resultCh <- teammateTaskResult{...}:
        case <-ctx.Done():
            return
        }
    }()
}
```

If `t.resultCh` is unbuffered and the reader has already exited (due to team deletion), this select will take the `<-ctx.Done()` branch (good). But if `resultCh` has buffer space, the send succeeds and nothing reads it. This is benign but worth noting.

More critically, in `idle_runner.go` the `runTeammateTask` function creates a full agent and runs it. If the parent context is cancelled, the agent's internal context propagation should handle cleanup, but there's no explicit `defer` for agent resource cleanup in the runner:

```go
func runTeammateTask(ctx context.Context, ...) string {
    // Creates agent, runs it, but no defer for cleanup
    agent := NewAgent(...)
    output, err := agent.Run(ctx, ...)
    // No defer agent.Close() or similar
}
```

**Impact:** Resources held by the teammate's agent (MCP connections, file handles) may not be cleaned up promptly on cancellation.

**Recommendation:** Add `defer` cleanup for agent resources in `runTeammateTask`. Verify that agent shutdown is comprehensive.

---

#### [M-01] Medium: `Manager` uses `map[string]*Team` without consistent lock ordering

**File:** `internal/swarm/manager.go`, lines 40-100

The `Manager` struct has `teams map[string]*Team` protected by `mu sync.Mutex`. Individual `Team` structs have their own `mu sync.Mutex`. If a caller holds `Manager.mu` and then acquires `Team.mu` (or vice versa), there is a potential deadlock. Currently the code appears to always acquire `Manager.mu` first, then `Team.mu`, which is correct. However, some methods like `Manager.TeammateResults()` access `Team` methods while holding `Manager.mu`:

```go
func (m *Manager) TeammateResults(teamID string, ...) (map[string]string, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    team, ok := m.teams[teamID]
    // ...
    return team.Results(teammateID) // team.Results acquires team.mu
}
```

This is safe as long as no `Team` method ever calls back into `Manager`. Verify this invariant holds.

**Impact:** Potential deadlock if lock ordering is violated in future changes.

**Recommendation:** Document the lock ordering invariant: `Manager.mu` -> `Team.mu`. Consider extracting the team lookup (read-only) to avoid holding `Manager.mu` during team operations.

---

### `internal/context/` (manager.go)

#### [M-02] Medium: `Summarize()` releases and re-acquires mutex between plan building and application

**File:** `internal/context/manager.go`, lines 365-433

`Summarize()` calls `buildSummaryPlan()` (which acquires and releases `m.mu`), then calls `summarizeMessages()` (no lock), then re-acquires `m.mu` to apply changes. Between `buildSummaryPlan()` returning and the re-acquisition, messages can be appended by concurrent `Add()` calls. The code handles this with a TOCTOU fix:

```go
var extraMsgs []provider.Message
if len(m.messages) > plan.origLen {
    extraMsgs = make([]provider.Message, len(m.messages)-plan.origLen)
    copy(extraMsgs, m.messages[plan.origLen:])
}
```

This is correct for appends, but if messages are **removed** or **mutated** between releases (e.g., another `Summarize()` call or `Clear()`), the index-based comparison could panic with an out-of-bounds access.

**Impact:** Theoretical panic if concurrent summarization or `Clear()` shrinks the message list below `plan.origLen`.

**Recommendation:** Add a bounds check: `if len(m.messages) > plan.origLen` already handles this case since messages can only grow through `Add()` in normal operation. However, add a defensive check for message list shrinking:

```go
if len(m.messages) < plan.origLen {
    // Messages were cleared or compacted concurrently; abort
    m.mu.Unlock()
    return fmt.Errorf("context changed during summarization")
}
```

---

#### [M-03] Medium: `compactTargetTokens()` and related methods called without lock

**File:** `internal/context/manager.go`, lines 785-810

Methods like `compactTargetTokens()`, `summaryReserveTokens()`, and `usablePromptBudgetLocked()` access `m.contextWindow` and `m.outputReserve` without acquiring `m.mu`. Their names include "Locked" suffix, indicating they must be called under lock, and they are consistently called from locked contexts in the current code. However, `buildSummaryPlan()` calls `m.countTokens()` which calls `m.provider.CountTokens()` under lock, which could be slow and block other operations.

**Impact:** Holding the mutex during potentially slow LLM token counting API calls blocks all context operations.

**Recommendation:** Consider caching provider token counts or using a read-write lock to allow concurrent reads during token counting.

---

### `internal/session/` (store.go)

#### [M-04] Medium: `Save()` creates temporary files that may leak on crash

**File:** `internal/session/store.go`, lines ~200-250

`Save()` writes to a temporary file and renames it atomically. If the process crashes between writing and renaming, the temporary file remains in the session directory:

```go
func (s *Store) Save(session *Session) error {
    // ...
    tmpPath := path + ".tmp"
    if err := os.WriteFile(tmpPath, data, 0600); err != nil {
        return err
    }
    return os.Rename(tmpPath, path)
}
```

**Impact:** Stale `.tmp` files accumulate on crash. Not a data integrity issue (the original file is intact) but a disk hygiene concern.

**Recommendation:** Add cleanup logic at startup to remove stale `.tmp` files from the session directory. This is defensive; not urgent.

---

#### [M-05] Medium: JSONL append is not atomic

**File:** `internal/session/store.go`, lines ~300-350

`AppendMessage()` opens the JSONL file in append mode and writes a line. If the process crashes mid-write, a partial JSON line may appear in the file:

```go
func (s *Store) AppendMessage(sessionID string, msg json.RawMessage) error {
    // ...
    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
    // ...
    if _, err := f.Write(append(msg, '\n')); err != nil {
        return err
    }
    return f.Sync() // fsync for durability
}
```

The `f.Sync()` is good for durability but does not prevent partial writes on crash. The JSONL reader should handle malformed lines gracefully.

**Impact:** Minor - the reader already uses line-by-line parsing and skips malformed entries. The `Sync()` call adds durability.

**Recommendation:** Verify that all JSONL readers gracefully skip malformed lines (spot-checked: they do via `continue` on `json.Unmarshal` errors). No action needed.

---

#### [M-06] Medium: `checkpointData` mutex not consistently used for all fields

**File:** `internal/session/store.go`, lines ~80-120

`checkpointData` has a `mu sync.Mutex` but some fields are accessed without it in read paths:

```go
type checkpointData struct {
    mu        sync.Mutex
    path      string
    data      []byte
    modTime   time.Time
    committed bool
}
```

The `committed` field is set under lock in `Commit()` but read without lock in `HasCheckpoint()`. Since `committed` is a boolean (single word), this is safe on most architectures but technically a data race under the Go memory model.

**Impact:** Theoretical data race on `committed` field. Benign in practice.

**Recommendation:** Use `atomic.Bool` for `committed`, or acquire the lock in `HasCheckpoint()`.

---

### `internal/knight/` (scheduler.go, budget.go, usage_tracker.go)

#### [M-07] Medium: `Knight` struct fields accessed without synchronization

**File:** `internal/knight/scheduler.go`, lines 40-80

The `Knight` struct has no mutex. Fields like `running`, `budget`, and `index` are accessed from multiple goroutines (scheduler goroutine + external API calls):

```go
type Knight struct {
    running   bool           // read/written from scheduler goroutine + CanPerformTask()
    budget    *BudgetTracker // accessed from scheduler + external
    index     *SkillIndex    // accessed from scheduler + external
    projDir   string         // immutable after construction
    // ...
}
```

`CanPerformTask()` reads `k.running` and `k.budget.CanSpend()` without any synchronization:

```go
func (k *Knight) CanPerformTask() bool {
    if k == nil || !k.running || !k.budget.CanSpend() {
        return false
    }
    return true
}
```

While `BudgetTracker` has its own mutex, `k.running` is a plain bool with no synchronization.

**Impact:** Data race on `k.running`. In practice, Knight runs in a single scheduler goroutine and external calls are rare, but this is technically a race.

**Recommendation:** Use `atomic.Bool` for `k.running`, or add a mutex to `Knight` with documented locking protocol.

---

#### [M-08] Medium: `appendSkillScenario()` read-rewrite-write is not concurrency-safe

**File:** `internal/knight/skill_scenario_log.go`, lines 75-98

`appendSkillScenario()` reads the entire JSONL file, appends an entry, truncates if needed, and writes the whole file back. If two goroutines call this concurrently (e.g., two sessions finishing simultaneously), one write may be lost:

```go
func (k *Knight) appendSkillScenario(entry SkillScenarioLogEntry) error {
    // ...
    entries, err := readSkillScenarios(path)
    // ...
    entries = append(entries, entry)
    // ...
    return util.AtomicWriteFile(path, []byte(b.String()), 0600)
}
```

**Impact:** Scenario log entries may be lost under concurrent access.

**Recommendation:** This is likely fine because Knight is typically single-threaded (one scheduler goroutine). But if concurrent recording is possible, add a mutex.

---

#### [M-09] Medium: `semanticMemoryStore` creates a new instance per call

**File:** `internal/knight/semantic_memory.go`, lines 151-157

`RecordSemanticMemory` and `RecentSemanticMemory` create a new `semanticMemoryStore` on every call:

```go
func (k *Knight) RecordSemanticMemory(...) error {
    store := newSemanticMemoryStore(k.semanticMemoryPath())
    return store.Append(...)
}
```

Each store instance has its own `sync.Mutex`. If two calls happen concurrently (from different goroutines), they use different mutexes and the file is unprotected.

**Impact:** Same as M-08 - potential data loss under concurrent access.

**Recommendation:** Store the `semanticMemoryStore` as a field on `Knight` so all calls share the same mutex.

---

### `internal/agent/` (agent.go, agent_tool.go, agent_autopilot.go, agent_compact.go)

#### [M-10] Medium: `Agent` struct has no mutex for `running`/`streaming` state

**File:** `internal/agent/agent.go`, lines 40-100

The `Agent` struct manages its running state through external synchronization (the caller holds a "run slot" mutex in the TUI/daemon). The agent itself does not protect internal state like `running`. This is acceptable given the single-owner model but should be documented.

The autopilot loop guard (`autopilotLoopGuardThreshold`) prevents infinite loops well:

```go
func (a *Agent) detectAutopilotLoop(messages []provider.Message) bool {
    // Counts consecutive identical tool calls, blocks after threshold
}
```

**Impact:** Low - the single-owner pattern is consistent across the codebase.

**Recommendation:** Document the threading model: "Agent instances are not thread-safe. Only one goroutine should call Run/RunStream at a time."

---

#### [M-11] Medium: Tool execution panic recovery may mask errors

**File:** `internal/agent/agent_tool.go`, lines ~80-120

Tool execution wraps the call in a panic recovery:

```go
func (a *Agent) executeTool(ctx context.Context, ...) (string, error) {
    defer func() {
        if r := recover(); r != nil {
            // Log panic and return error
        }
    }()
    // ...
}
```

This is good practice, but the panic recovery returns a generic error that may not include enough context for debugging.

**Impact:** Masked panics make debugging harder.

**Recommendation:** Include the stack trace in the error output (currently done via `runtimedebug.Stack()` - verified on re-read, this is correct).

---

### `internal/provider/` (openai.go, anthropic.go, gemini.go, retry.go)

#### [L-01] Low: OpenAI `ChatStream` does not validate response before accessing fields

**File:** `internal/provider/openai.go`, lines ~300-400

The streaming response handler accesses `response.Choices` without nil check in some code paths. While the OpenAI SDK generally guarantees non-nil choices, a nil response from the API could cause a panic.

**Impact:** Low - the OpenAI SDK handles this internally.

**Recommendation:** Add defensive nil checks for robustness.

---

#### [L-02] Low: Retry logic does not distinguish between retriable and non-retriable errors

**File:** `internal/provider/retry.go`, lines 30-100

`withRetry` retries on any error that `isRetriable` returns true for. The classification is based on HTTP status codes and error message patterns. However, some API errors (e.g., invalid model name returning 400) are not retriable but may match the pattern:

```go
func isRetriable(err error) bool {
    // Checks for 429, 500, 502, 503, 504, connection refused, timeout, etc.
}
```

400 errors are correctly excluded. The classification looks reasonable.

**Impact:** Low - the retry logic covers the common cases well.

**Recommendation:** No action needed.

---

#### [L-03] Low: `adaptiveCap` uses `sync.Mutex` for frequently read values

**File:** `internal/provider/adaptive_cap.go`

`AdaptiveCap` uses `sync.Mutex` for all operations including reads. The current usage pattern is low-frequency (rate limit headers from API calls), so this is fine. For high-frequency reads, `sync.RWMutex` would be better.

**Impact:** Negligible performance impact.

**Recommendation:** No action needed for current usage patterns.

---

### `internal/permission/` (mode.go, policy.go, config_policy.go, sandbox.go)

#### [L-04] Low: `ConfigPolicy.SetOverride` has no removal mechanism

**File:** `internal/permission/config_policy.go`, lines ~80-120

`SetOverride(toolName string, decision Decision)` allows setting per-tool overrides but there's no way to remove an override (reset to default). The override map grows indefinitely.

**Impact:** Minor memory leak in long-running sessions with many tool permission changes.

**Recommendation:** Add a `ClearOverride(toolName string)` method or reset mechanism.

---

#### [L-05] Low: `PathSandbox` is immutable after creation - safe but inflexible

**File:** `internal/permission/sandbox.go`

`PathSandbox` takes `allowedDirs` at construction and only provides read access. This is safe for concurrency (no mutable state) but means the allowed directories cannot be updated at runtime.

**Impact:** No safety issue. Design choice, not a bug.

**Recommendation:** No action needed.

---

### `internal/tool/` (various)

#### [L-06] Low: `atomicWrite.go` creates `.tmp` files that may leak

**File:** `internal/tool/atomic_write.go`

Similar to the session store issue. The `AtomicWriteFile` function writes to a temp file and renames. Crashes leave stale temp files.

**Impact:** Minor disk hygiene issue.

**Recommendation:** Same as M-04 - add startup cleanup or accept as-is.

---

#### [L-07] Low: `command_gate.go` rate limiter may be too aggressive

**File:** `internal/tool/command_gate.go`

The command execution gate uses a simple rate limiter. In burst scenarios (autopilot mode with many quick commands), the rate limiter may unnecessarily delay operations.

**Impact:** Minor UX degradation in autopilot mode.

**Recommendation:** Consider burst allowance or mode-aware rate limiting.

---

### `internal/swarm/` (continued)

#### [L-08] Low: Teammate inbox channel size is hardcoded

**File:** `internal/swarm/team.go`, line ~40

The teammate inbox channel is created with a fixed buffer size. If a teammate receives many tasks rapidly, the channel may fill and block senders:

```go
inbox: make(chan swarmTask, 16),
```

**Impact:** Task assignment may block if a teammate's inbox fills up.

**Recommendation:** Consider making the buffer size configurable or using an unbounded queue with backpressure.

---

### `internal/knight/` (continued)

#### [L-09] Low: `BudgetTracker` uses `time.Now().Sub()` for daily bucket rotation

**File:** `internal/knight/budget_buckets.go`

The daily bucket comparison uses wall clock time, which can be affected by clock adjustments (NTP changes, daylight saving). For a daily token budget, this is acceptable.

**Impact:** Negligible - daily reset timing may be off by seconds on clock adjustment.

**Recommendation:** No action needed.

---

#### [L-10] Low: Knight skill validation runs `exec.LookPath` for dependency checks

**File:** `internal/knight/skill_validator.go`, line 79

`checkDependencies` calls `exec.LookPath` for each required command. This is a filesystem operation that could be slow for many dependencies.

**Impact:** Minor - only runs during skill validation, not in the hot path.

**Recommendation:** Cache results if validation is called frequently.

---

## Cross-Cutting Concerns

### Goroutine Lifecycle Management

The codebase generally follows good patterns for goroutine lifecycle:
- Context cancellation is propagated consistently
- Sub-agent and swarm goroutines derive contexts from parent
- `CancelAll()` cascades cancellation properly

**Notable gap:** The metrics `Collector` goroutine (C-01) has a broken shutdown path.

### Mutex Usage Patterns

Mutex usage is consistent across packages:
- `internal/context/Manager`: Single mutex, always acquired before accessing fields
- `internal/subagent/Manager`: Single mutex for agent map
- `internal/swarm/Manager` + `Team`: Two-level locking with consistent ordering
- `internal/knight/budget.BudgetTracker`: Has its own mutex, properly used
- `internal/knight/skillIndex`: Has its own mutex, properly used

**Notable gap:** `Knight` struct itself has no synchronization for `running` field.

### Error Handling

Error handling is thorough:
- Provider errors are wrapped with context (`fmt.Errorf("...: %w", err)`)
- Retry logic correctly handles transient vs permanent failures
- Tool execution has panic recovery with stack traces

### Resource Cleanup

- Session file handles are closed via `defer`
- Sub-agent resources are cleaned up on cancellation
- Knight skill index files are properly managed

**Notable gap:** Swarm teammate agent cleanup on cancellation (H-03).

---

## Summary Table

| ID | Severity | Package | File | Issue |
|----|----------|---------|------|-------|
| H-01 | High | metrics | collector.go | Goroutine leak if `Stop()` never called (no context cancellation fallback) |
| H-02 | High | subagent | manager.go | Goroutine may run after ctx cancellation (mitigated by buffer-1 channel) |
| H-03 | High | subagent | manager.go | `CancelAll()` returns without waiting for goroutine termination |
| H-04 | High | swarm | team.go, idle_runner.go | Teammate agent resources not cleaned up on cancellation |
| M-01 | Medium | swarm | manager.go | Two-level locking (Manager.mu -> Team.mu) should be documented |
| M-02 | Medium | context | manager.go | `Summarize()` TOCTOU window between plan and apply - missing shrink check |
| M-03 | Medium | context | manager.go | Mutex held during slow `CountTokens` API calls |
| M-04 | Medium | session | store.go | Stale `.tmp` files may accumulate on crash |
| M-05 | Medium | session | store.go | JSONL append not atomic; reader handles gracefully |
| M-06 | Medium | session | store.go | `checkpointData.committed` accessed without lock |
| M-07 | Medium | knight | scheduler.go | `Knight.running` bool accessed without synchronization |
| M-08 | Medium | knight | skill_scenario_log.go | `appendSkillScenario` read-rewrite-write not concurrency-safe |
| M-09 | Medium | knight | semantic_memory.go | New `semanticMemoryStore` per call defeats mutex purpose |
| M-10 | Medium | agent | agent.go | No mutex for running state (acceptable single-owner model) |
| M-11 | Medium | agent | agent_tool.go | Panic recovery in tool execution (properly handled) |
| L-01 | Low | provider | openai.go | Missing nil check for streaming response |
| L-02 | Low | provider | retry.go | Retry classification covers common cases adequately |
| L-03 | Low | provider | adaptive_cap.go | Uses Mutex instead of RWMutex (low frequency reads) |
| L-04 | Low | permission | config_policy.go | No mechanism to remove per-tool overrides |
| L-05 | Low | permission | sandbox.go | Immutable sandbox is safe but inflexible |
| L-06 | Low | tool | atomic_write.go | Stale temp files on crash |
| L-07 | Low | tool | command_gate.go | Rate limiter may be too aggressive in autopilot |
| L-08 | Low | swarm | team.go | Hardcoded inbox channel buffer size |
| L-09 | Low | knight | budget_buckets.go | Wall clock time for daily bucket rotation |
| L-10 | Low | knight | skill_validator.go | `exec.LookPath` called per dependency |

---

## Recommendations (Priority Order)

1. **Fix H-01** - Add context.Context to Collector constructor so goroutine exits even without Stop().
2. **Address H-04** - Add `defer` cleanup for teammate agent resources in `runTeammateTask`.
3. **Address H-03** - Add WaitGroup or document async cancellation semantics in `CancelAll()`.
4. **Address H-02** - Document that sub-agent goroutines run briefly after cancellation (buffer-1 channel prevents permanent leak).
5. **Address M-07** - Use `atomic.Bool` for `Knight.running`.
6. **Address M-09** - Store `semanticMemoryStore` as a Knight field.
7. **Address M-02** - Add defensive bounds check in `Summarize()` for concurrent mutation.
8. **Address M-06** - Use `atomic.Bool` for `checkpointData.committed`.
9. Consider M-03 for performance optimization in high-frequency compaction scenarios.
