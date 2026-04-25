# A2A Implementation Review

## Architecture Overview

```
                    ┌──────────────────────────────────────────┐
                    │              ggcode Instance A            │
                    │                                          │
  External MCP ────►│  MCPBridgeTools ─► Client ──HTTP──┐     │
  Client            │                                    │     │
                    │  RemoteTool ──► Registry ──file──┐ │     │
                    │                                  │ │     │
                    │          ┌──── Server ◄──────────┘ │     │
                    │          │   (HTTP, JSON-RPC)       │     │
                    │          │                          │     │
                    │          └──► TaskHandler ──► Agent │     │
                    │                    │       ──► Tool │     │
                    │               instances.json        │     │
                    └──────────────────────────────────────────┘
```

## Issues by Severity

---

## 🔴 High — Correctness / Security

### H1. `handleMessageSend` busy-polls with 100ms ticker — wastes CPU, no context propagation

**File:** `server.go:237-260`

```go
ticker := time.NewTicker(100 * time.Millisecond)
for {
    select {
    case <-ticker.C:
        t, ok := s.handler.GetTask(task.ID)  // lock+unlock every 100ms
    case <-deadline:
        ...
    }
}
```

**Problems:**
1. **No `r.Context()` propagation** — `Handle()` uses `context.Background()`. If the HTTP client disconnects, the server won't notice and keeps polling (and keeps the agent running) until the 5-minute timeout.
2. **100ms polling is wasteful** — For a 5-minute task, this does 3,000 mutex lock+unlock cycles.
3. **Lock contention** — `GetTask()` acquires `h.mu` every 100ms while `execute()` and `updateStatus()` also need it.

**Fix:** Use a `sync.Cond` or per-task notification channel instead of polling:
```go
// In Task struct:
done chan struct{} // closed when task reaches terminal state

// In handleMessageSend:
select {
case <-task.done:
    t, _ := s.handler.GetTask(task.ID)
    writeRPCResult(w, req.ID, t)
case <-r.Context().Done():
    // client disconnected
case <-deadline:
    ...
}
```

### H2. `handleMessageStream` / `handleTaskResubscribe` — same polling problem + no timeout

**File:** `server.go:298-316, 394-411`

Both SSE streaming handlers poll at 200ms with **no timeout**. If the agent hangs forever, these handlers hold an HTTP connection open forever.

**Fix:** Add the same deadline as `handleMessageSend` + propagate `r.Context()`.

### H3. `handleTaskCancel` returns stale task data after cancellation

**File:** `server.go:335-357`

```go
func (s *Server) handleTaskCancel(w http.ResponseWriter, req *JSONRPCRequest) {
    task, ok := s.handler.GetTask(params.ID)  // snapshot BEFORE cancel
    // ...
    s.handler.CancelTask(params.ID)            // mutates task
    writeRPCResult(w, req.ID, task)            // returns STALE snapshot
}
```

The task snapshot is taken before `CancelTask()` mutates it. The response shows the old state (e.g., "working"), not "canceled".

**Fix:** Fetch the task again after cancel, or have `CancelTask` return the updated task.

### H4. `generateID()` is not collision-safe

**File:** `handler.go:504-506`

```go
func generateID() string {
    return fmt.Sprintf("%d", time.Now().UnixNano())
}
```

Two concurrent requests in the same nanosecond produce the same ID. On Go 1.26 with multiple goroutines, this is plausible under load.

**Fix:** Use `crypto/rand` or at minimum `fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddInt64(&seq, 1))`.

### H5. `GetTask` returns a pointer to internal mutable state

**File:** `handler.go:324-329`

```go
func (h *TaskHandler) GetTask(id string) (*Task, bool) {
    h.mu.Lock()
    defer h.mu.Unlock()
    t, ok := h.tasks[id]
    return t, ok  // returns pointer to shared mutable Task
}
```

Callers get a pointer to the Task inside the map. If the background goroutine calls `updateStatus()` concurrently, the caller sees a partially-mutated Task (data race on `t.Status`, `t.Artifacts`, etc.). This affects all `handleTaskGet`, `handleMessageSend` polling, and SSE streaming.

**Fix:** Return a deep copy (snapshot) like `task.Snapshot()`, or make `GetTask` return `Task` by value.

---

## 🟠 Medium — Performance / Design

### M1. `detectWorkspaceMeta` walks the entire project tree synchronously in constructor

**File:** `registry.go:220-286`

```go
func NewTaskHandler(workspace string, ...) *TaskHandler {
    // ...
    meta: detectWorkspaceMeta(workspace),  // walks EVERY file in workspace
}
```

`detectWorkspaceMeta` does `filepath.WalkDir` across the entire workspace to detect languages. In a large project this takes 100ms-1s+ and blocks the caller.

**Fix:** Run detection asynchronously, start with an empty meta, populate when ready.

### M2. Registry `load()`/`save()` does full file read-modify-write every operation

**File:** `registry.go:161-182`

Every `Register()`, `Unregister()`, `Discover()`, and `UpdateStatus()` reads the entire JSON file, modifies it in memory, and writes it back. Under concurrent access from multiple ggcode instances, this is prone to lost updates (two instances read, both modify, one write wins).

**Fix:** Use file locking (`flock` or `fcntl`), or use per-instance files (one file per PID).

### M3. `RemoteTool.findInstance` matches first result — no disambiguation

**File:** `remote_tool.go:144-164`

```go
for _, inst := range instances {
    name := strings.ToLower(filepath.Base(inst.Workspace))
    if name == target || strings.Contains(name, target) {
        return &inst, nil  // returns FIRST match
    }
}
```

If multiple instances match (e.g., two "order-service" instances), the tool silently picks the first one. The agent has no way to choose or even know there are multiple.

**Fix:** If multiple matches, return an error listing all candidates.

### M4. `continueTask` has TOCTOU on task state check

**File:** `handler.go:158-186`

```go
task.Status.State != TaskStateInputRequired  // check under lock
// ...
go h.execute(taskCtx, task, perm)  // goroutine reads task state without lock
```

Between the state check and the goroutine starting, the task could be canceled. The goroutine starts executing regardless.

**Fix:** Set status to `TaskStateWorking` inside the locked section before starting the goroutine.

### M5. Task map grows unboundedly — no cleanup of old completed tasks

**File:** `handler.go:142`

```go
h.tasks[task.ID] = task  // never deleted except by CancelTask
```

Completed, failed, and canceled tasks stay in the map forever. A long-running ggcode instance accumulates tasks until OOM.

**Fix:** Add periodic cleanup (delete tasks older than N minutes from the terminal state) or a LRU with a cap.

### M6. SSE `decodeSSE` doesn't handle multi-line `data:` fields

**File:** `client.go:264-281`

```go
for scanner.Scan() {
    line := scanner.Text()
    if !strings.HasPrefix(line, "data: ") {
        continue  // skips comment lines, multi-line data, etc.
    }
```

Per SSE spec, `data:` can span multiple lines (each starting with `data:`), separated by `\n`. Also, lines starting with `:` are comments and should be ignored. `event:`, `id:`, `retry:` fields are silently discarded. If the server ever sends multi-line data (e.g., large JSON with newlines), the client silently drops content.

### M7. Client hardcodes JSON-RPC ID as `1`

**File:** `client.go:219`

```go
ID: json.RawMessage(`1`),
```

All requests use the same ID. If the client ever supports concurrent requests over a shared connection (future), responses can't be matched. Not a bug today but limits extensibility.

### M8. `RemoteTool.Execute` sends fire-and-forget — no wait for completion

**File:** `remote_tool.go:86-92`

```go
task, err := client.SendMessage(ctx, params.Skill, params.Message)
// Returns immediately with "Task sent" — no waiting for actual result
```

`SendMessage` via `message/send` on the server does wait for completion. But the tool returns `task.Status.State` which might be "working" (the server's `handleMessageSend` waits, but the response comes after completion). This is actually OK because the server waits synchronously. But the tool's output says "Task sent to..." which implies async — misleading.

---

## 🟡 Low — Code Quality

### L1. `pickToolForSkill` heuristic is fragile

**File:** `handler.go:442-463`

```go
case SkillFileSearch:
    if strings.Contains(input, "*") || strings.Contains(input, ".") {
        return "glob"
    }
    return "search_files"
```

Input "find the main.go file" contains "." → selects `glob`. But the user wanted content search. This heuristic is too simplistic. Since `executeAgent` is preferred when available, this only matters for the no-agent fallback.

### L2. `a2aDiscoverTool.Execute` ignores `ctx`

**File:** `mcp_bridge.go:49`

`Execute(ctx context.Context, input ...)` receives a context but `t.client.Discover(ctx)` uses it. OK actually — this one is fine.

### L3. `RemoteTool` skill enum in schema doesn't match `skillPermissions` keys

**File:** `remote_tool.go:51`

The schema hardcodes `["code-edit", "file-search", "command-exec", "git-ops", "code-review", "full-task"]`. If new skills are added to `skillPermissions`, the schema won't list them. Consider deriving from `skillPermissions` keys.

---

## Summary

| Severity | Count | Key Issues |
|----------|-------|-----------|
| 🔴 High | 5 | Polling instead of notification (H1), no timeout on SSE (H2), stale cancel response (H3), ID collision (H4), data race on GetTask pointer (H5) |
| 🟠 Medium | 8 | Synchronous workspace walk (M1), registry file race (M2), no disambiguation (M3), TOCTOU on continue (M4), unbounded task map (M5), incomplete SSE parsing (M6) |
| 🟡 Low | 3 | Fragile tool heuristic (L1), schema/permissions drift (L3) |

## Top 3 Fixes by Impact

1. **H5 (data race) → H1 (polling):** Replace the polling pattern with per-task notification channels. This fixes both the data race (by not needing to poll shared state) and the CPU waste. Add `r.Context()` propagation so client disconnects cancel the work.

2. **M5 (unbounded task map):** Add a background goroutine or timer to evict completed tasks older than 30 minutes. Without this, a long-running server leaks memory.

3. **M2 (registry file race):** Use per-instance files (`instances/<pid>.json`) instead of a shared `instances.json`. Eliminates cross-process write contention entirely.
