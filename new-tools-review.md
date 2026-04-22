# New Built-in Tools Code Review

## Files Reviewed

| File | Status | Purpose |
|------|--------|---------|
| `internal/tool/config_tool.go` | **New** | Runtime config read/write tool |
| `internal/tool/cron_tools.go` | **New** | Cron job scheduling tools (create/delete/list) |
| `internal/tool/task_tools.go` | **New** | Session-scoped task tracking (create/get/list/update/stop/output) |
| `internal/tool/send_message.go` | **New** | Send messages to sub-agents |
| `internal/tool/plan_mode_tools.go` | **New** | Enter/exit plan mode |
| `internal/tool/atomic_write.go` | **New** | Alias for util.AtomicWriteFile |
| `internal/cron/scheduler.go` | **New** | In-memory cron scheduler |
| `internal/cron/scheduler_test.go` | **New** | Scheduler tests |
| `internal/task/manager.go` | **New** | In-memory task manager |
| `internal/task/manager_test.go` | **New** | Task manager tests |
| `internal/tui/repl.go` | **Modified** | Wiring for all new tools |
| `internal/tui/model_update.go` | **Modified** | modeChangeMsg, cronPromptMsg handlers |
| `internal/tui/model_pending.go` | **Existing** | Pending submission queue |
| `internal/permission/mode.go` | **Modified** | IsReadOnlyTool, ParsePermissionMode |
| `internal/permission/config_policy.go` | **Modified** | Plan mode + isModeControlTool |
| `internal/subagent/manager.go` | **Modified** | SendToAgent, Broadcast, GetTaskOutput |
| `internal/tool/lsp.go` | **Modified** | lsp_call_hierarchy tools added to read-only |

---

## 🔴 Issues That Need Attention

### 1. Cron `scheduleJob` recursive call while holding `s.mu` — deadlock

**File:** `internal/cron/scheduler.go:125-144`

```go
func (s *Scheduler) scheduleJob(job *Job, interval time.Duration) {
    timer := time.AfterFunc(interval, func() {
        s.enqueue(job.Prompt)       // line 127: may call SetEnqueue callback

        s.mu.Lock()                 // line 129: acquire mutex
        defer s.mu.Unlock()

        if job.Recurring {
            job.NextFire = time.Now().Add(interval)
            s.scheduleJob(job, interval)  // line 134: recursive call
        }
    })

    s.mu.Lock()                     // line 141: acquire mutex AGAIN
    s.timers[job.ID] = timer
    s.mu.Unlock()                   // line 143
}
```

The `time.AfterFunc` callback at line 129 acquires `s.mu`, then calls `s.scheduleJob` at line 134, which at lines 141-143 tries to acquire `s.mu` again. Since Go's `sync.Mutex` is not reentrant, this is a **deadlock**.

The first call at `Create()` (line 71) is fine because `s.mu` is not held (unlocked at line 69). But the recursive call from inside the timer callback holds the lock.

**Fix:** Move the timer registration outside the lock, or use a separate helper that doesn't lock:
```go
func (s *Scheduler) scheduleJobLocked(job *Job, interval time.Duration) {
    // must be called with s.mu held
    timer := time.AfterFunc(interval, func() {
        s.fireJob(job, interval)
    })
    s.timers[job.ID] = timer
    job.NextFire = time.Now().Add(interval)
}
```

### 2. Cron `enqueue` callback called while holding no lock but may call `SetEnqueue`

**File:** `internal/cron/scheduler.go:127`

```go
s.enqueue(job.Prompt)  // called BEFORE s.mu.Lock()
```

The `enqueue` callback (set via `SetEnqueue`) is `r.program.Send(cronPromptMsg{...})` (repl.go:181-184). `program.Send` is safe to call from any goroutine. However, if `SetEnqueue` is called concurrently (line 117-123), the callback itself could be swapped mid-call. In practice this is safe because Go function values are atomic to read, but worth noting.

### 3. `TaskStop` has TOCTOU race — reads status then updates

**File:** `internal/tool/task_tools.go:220-242`

```go
func (t TaskStopTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
    // ...
    tk, ok := t.Manager.Get(args.TaskID)      // read snapshot (lock+unlock)
    if tk.Status != task.StatusInProgress {    // check stale snapshot
        return Result{IsError: true, ...}, nil
    }
    // Between Get and Update, another goroutine could change status
    updated, err := t.Manager.Update(args.TaskID, task.UpdateOptions{Status: &pending})
```

The status check is performed on a snapshot, then the update happens in a separate lock acquisition. Between the two, another goroutine could complete or cancel the task. The update would then silently reset a completed task back to pending.

**Fix:** Add a `ConditionalUpdate(taskID, expectedStatus, opts)` method to Manager, or move the status validation into the Update method:
```go
// In Manager.Update:
if opts.Status != nil {
    if *opts.Status == StatusPending && t.Status == StatusCompleted {
        return Task{}, fmt.Errorf("cannot reset completed task to pending")
    }
}
```

Or simpler: just remove the pre-check from TaskStopTool and let Manager.Update validate the transition.

### 4. `ExitPlanModeTool` mode parsing accepts empty string as "supervised"

**File:** `internal/tool/plan_mode_tools.go:74-81`

```go
mode := t.DefaultMode
if args.Mode != "" {
    parsed := permission.ParsePermissionMode(args.Mode)
    if parsed == permission.SupervisedMode && args.Mode != "" && args.Mode != "supervised" {
        return Result{IsError: true, ...}, nil
    }
    mode = parsed
}
```

The condition `parsed == permission.SupervisedMode && args.Mode != "" && args.Mode != "supervised"` is meant to catch invalid modes that fall through to the `default` case of `ParsePermissionMode`. But `ParsePermissionMode` returns `SupervisedMode` for any unrecognized string, so `args.Mode = "bypasss"` (typo) would return `SupervisedMode` and the check would catch it. However, `args.Mode = "PLAN"` would parse as `SupervisedMode` (case-insensitive only matches `"plan"` → `PlanMode`), and the check would also catch it. The logic works but is fragile — consider using an explicit allowlist or a `bool ok` return from `ParsePermissionMode`.

### 5. `TaskOutputTool` has `block` and `timeout` params declared but unused

**File:** `internal/tool/task_tools.go:260-292`

The JSON schema declares:
```json
"block": {"type": "boolean", "description": "Whether to wait for completion (default true)"},
"timeout": {"type": "integer", "description": "Max wait time in ms (default 30000)"}
```

But `Execute` only does a single non-blocking lookup:
```go
output, found := t.Provider.GetTaskOutput(args.TaskID)
```

The `block` and `timeout` params are parsed but never used. The LLM will think it can poll for in-progress tasks, but it always gets a snapshot.

**Note:** This might be intentional — "V1, will implement blocking later." But the schema should not advertise features that don't work, as the LLM will rely on them.

### 6. `replModeSwitcher` holds raw `*Model` pointer — value receiver copies model

**File:** `internal/tui/repl.go:211-213`

```go
type replModeSwitcher struct {
    model *Model
}
```

`replModeSwitcher` holds a `*Model` pointer. This is correct — it points to the original model, not a Bubble Tea copy. But `SetMode` is called from the agent goroutine, not the TUI thread:

```go
func (s replModeSwitcher) SetMode(mode permission.PermissionMode) {
    if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
        cp.SetMode(mode)  // thread-safe (has own mutex)
    }
    if s.model.program != nil {
        s.model.program.Send(modeChangeMsg{Mode: mode})  // async safe
    }
}
```

This is safe because `ConfigPolicy.SetMode` has its own mutex and `program.Send` is goroutine-safe. ✅

---

## 🟡 Medium Issues

### 7. `ConfigTool` value receiver on struct with interface field

**File:** `internal/tool/config_tool.go:24`

```go
type ConfigTool struct {
    Access ConfigAccess  // interface field
}
```

All methods use value receivers (`func (t ConfigTool)`). If `ConfigAccess` implementations have mutable state, a value copy could lose it. In this case, `replConfigAccess` holds a `*Model` pointer, so the copy is fine. But it's fragile — a future `ConfigAccess` implementation that uses value state would break silently.

### 8. Cron `Delete` stops timer then checks job existence — harmless but backwards

**File:** `internal/cron/scheduler.go:77-90`

```go
func (s *Scheduler) Delete(id string) bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    if timer, ok := s.timers[id]; ok {
        timer.Stop()           // stop timer first
        delete(s.timers, id)
    }
    if _, ok := s.jobs[id]; !ok {
        return false           // then check if job exists
    }
    delete(s.jobs, id)
    return true
}
```

If a timer exists for an ID but the job doesn't (shouldn't happen but defensive), the timer is stopped but `false` is returned. This means the caller thinks delete failed, but the timer was actually stopped. Should probably check job existence first.

### 9. `send_message.go` broadcasts to running agents — reads `sa.Status` without `sa.mu`

**File:** `internal/subagent/manager.go:384-399`

```go
func (m *Manager) Broadcast(msg AgentMessage) []string {
    m.mu.Lock()
    defer m.mu.Unlock()
    for _, sa := range m.agents {
        if sa.Status == StatusRunning {  // reads Status without sa.mu
```

Same issue as L1 in locks.md — `Status` is written under `sa.mu` but read under only `m.mu`.

### 10. Cron `enqueue` callback may fire after TUI exits

**File:** `internal/cron/scheduler.go:126-127`

The `time.AfterFunc` fires on a goroutine independent of the TUI lifecycle. If the TUI exits:
- `r.program` is nil → `program.Send` would panic
- The nil check `if r.program != nil` in repl.go:182 protects against this

But the scheduler itself has no shutdown method. Timers keep running until they fire and find no program. For one-shot jobs this is fine (self-delete), but recurring jobs will keep firing into the void forever.

**Fix:** Add a `Scheduler.Shutdown()` method that stops all timers and clears jobs.

### 11. `task.Manager.Delete` doesn't clean up dangling block references

**File:** `internal/task/manager.go:209-218`

```go
func (m *Manager) Delete(taskID string) bool {
    // ...
    delete(m.tasks, taskID)
    return true
}
```

If task A blocks task B, and A is deleted, B's `BlockedBy` still references A's ID. Future `task_list` output will show stale dependency references.

**Fix:** Clean up reverse references on delete:
```go
// Remove taskID from all other tasks' Blocks and BlockedBy lists
for _, other := range m.tasks {
    other.Blocks = removeString(other.Blocks, taskID)
    other.BlockedBy = removeString(other.BlockedBy, taskID)
}
```

---

## 🟢 Good Patterns

### ✅ Consistent tool structure
All tools follow the same pattern: struct with dependency injection → `Name()/Description()/Parameters()/Execute()` → JSON schema as raw message → unmarshal into typed args → validate → delegate to manager. Clean and uniform.

### ✅ Proper Snapshot pattern in task.Manager
`Task.Snapshot()` deep-copies slices and maps, preventing callers from mutating internal state. `Get()` and `List()` return snapshots. This is correct.

### ✅ Cron scheduler uses callback injection
`NewScheduler(enqueue)` takes a callback, then `SetEnqueue` allows late binding. This decouples the scheduler from the TUI lifecycle.

### ✅ Plan mode tool uses `program.Send` for thread safety
`replModeSwitcher.SetMode` doesn't directly mutate `m.mode` from the agent goroutine — it sends a `modeChangeMsg` to the TUI event loop. Correct Bubble Tea pattern.

### ✅ Permission-aware plan mode integration
`isModeControlTool` allows `enter_plan_mode`/`exit_plan_mode` even in plan mode (so you can exit). `IsReadOnlyTool` includes all LSP read-only tools. `config_policy.go` checks sandbox even in plan mode for file tools.

### ✅ Non-blocking mailbox sends
`SendToAgent` uses `select { case sa.Mailbox <- msg: ... default: return error }`. This never blocks the caller if the mailbox is full. Same for `Broadcast`.

---

## Summary

| Severity | Count | Key Issues |
|----------|-------|-----------|
| 🔴 High | 2 | Cron scheduler deadlock (#1), TaskStop TOCTOU race (#3) |
| 🟡 Medium | 6 | Unused schema params (#5), no scheduler shutdown (#10), dangling block refs (#11), Status read without sa.mu (#9), Delete ordering (#8), mode parsing fragility (#4) |
| 🟢 Good | 6 | Consistent patterns, snapshots, thread-safe mode switching, non-blocking sends |

**Most urgent:** The cron scheduler deadlock (#1) will hang the first time a recurring job reschedules itself. Simple fix: split `scheduleJob` into a locked and unlocked variant.
