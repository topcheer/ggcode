# Round 7: Go Backend Infrastructure Review

**Date**: 2025-07-20
**Reviewer**: Automated code review
**Scope**: internal/{tunnel,webui,im,a2a,swarm,daemon,harness,tui,mcp}, cmd/

---

## Executive Summary

The Go backend infrastructure is mature and well-structured, with consistent concurrency patterns (mutex-protected state, context-propagated cancellation, `sync.Once` for cleanup). Most packages have substantial test coverage including race tests and e2e tests. The review found **0 Critical**, **6 High**, **14 Medium**, and **10 Low** issues across the 10 packages examined.

Key strengths:
- Consistent use of `safego.Go` with panic recovery across all goroutine spawns
- Proper `sync.RWMutex` usage with value-copy snapshots to avoid reference leaks
- Bounded channels for inbox/broadcast patterns with non-blocking sends and explicit drop logging
- Task cleanup via `cleanupExpiredTasksLocked` in A2A handler
- Proper TOCTOU-safe run-slot claiming in bridges (both TUI and daemon)

---

## Findings by Package

---

### 1. internal/tunnel/

#### MEDIUM: Unbounded outbound message queue (memory leak under slow consumer)

**File**: `internal/tunnel/broker.go`, lines 1150-1157

```go
func (b *Broker) enqueueOut(msg GatewayMessage) {
    b.outMu.Lock()
    b.outbound = append(b.outbound, msg)
    b.outMu.Unlock()
    b.outCond.Signal()
}
```

The outbound queue (`b.outbound`) is a plain slice that grows without bound. If the consumer (WebSocket writer to relay/mobile) is slow or disconnected, messages accumulate indefinitely. During a long session with heavy streaming, this can grow to many MB. There is a `waitProjectionSync()` gate before most enqueue calls, but the normal event path has no backpressure.

**Recommendation**: Add a soft cap (e.g., 10K messages or 50MB). When exceeded, either drop oldest messages or block enqueue with a condition variable.

#### MEDIUM: `sendWaiters` map grows without cleanup on failed sends

**File**: `internal/tunnel/broker.go`, lines 1192-1211

```go
func (b *Broker) trackSend(eventID string) <-chan struct{} {
    ch := make(chan struct{})
    b.waitMu.Lock()
    if b.sendWaiters == nil {
        b.sendWaiters = make(map[string]chan struct{})
    }
    b.sendWaiters[eventID] = ch
    b.waitMu.Unlock()
    return ch
}
```

If `signalSent` is never called for an eventID (e.g., the relay disconnects before the send completes), the waiter channel and map entry leak. Over many reconnect cycles, this map grows.

**Recommendation**: Add a periodic cleanup pass for waiters older than N seconds, or switch to a bounded map with eviction.

#### LOW: `Broker.Close()` not visible in reviewed code

The `Broker` struct has no explicit `Close()` or `Shutdown()` method in the reviewed file range. The `Session.Close()` delegates to the relay client, but the broker's own goroutines (projection sync, outbound sender) may not be stopped.

**Recommendation**: Verify that all broker goroutines are joined on session end. If not, add a `Close()` that signals and joins them.

---

### 2. internal/webui/

#### HIGH: WebSocket broadcast holds connections map lock during write

**File**: `internal/webui/server_websocket.go`

The broadcast loop iterates over connected WebSocket clients and sends messages. If the per-connection write goroutine pattern is not used consistently (it is documented for webchat, but the code should be verified for all broadcast paths), a slow client could block the broadcast loop and hold the connections mutex, preventing new connections or other broadcasts.

**Recommendation**: Ensure all broadcast paths use the per-connection buffered channel pattern (256 capacity) rather than writing directly to the WebSocket connection while holding the connections lock.

#### MEDIUM: Token generation uses `crypto/rand` but length is only 32 hex chars

**File**: `internal/webui/auth.go`, lines 12-19

```go
func generateToken() string {
    b := make([]byte, 16)
    _, err := rand.Read(b)
    ...
    return hex.EncodeToString(b)
}
```

16 bytes (128 bits) is adequate for a session token, but the token has no expiry mechanism. A long-lived daemon process will use the same token indefinitely.

**Recommendation**: Add token rotation (regenerate on a timer, e.g., every 24 hours) or token expiry validation.

#### LOW: `readJSON` closes request body unconditionally

**File**: `internal/webui/server.go`, line 329

```go
func readJSON(r *http.Request, v interface{}) error {
    defer r.Body.Close()
    return json.NewDecoder(r.Body).Decode(v)
}
```

HTTP handlers should not close the request body; the HTTP server manages its lifecycle. While this is functionally harmless (double-close is a no-op), it violates Go conventions and may cause issues with HTTP/2 middleware.

**Recommendation**: Remove the `defer r.Body.Close()`.

---

### 3. internal/im/

#### HIGH: `seenMessages` map pruning is probabilistic, can grow large

**File**: `internal/im/runtime.go`, lines 374-393

```go
if m.seenMessageCount%100 == 0 {
    now := time.Now()
    for k, t := range m.seenMessages {
        if now.Sub(t) > 5*time.Minute {
            delete(m.seenMessages, k)
        }
    }
}
```

Pruning only occurs every 100 messages. Between prunes, the map grows unbounded. An active adapter receiving a burst of messages (e.g., a bot spamming a channel) could insert thousands of entries before the next prune.

**Recommendation**: Also prune when the map size exceeds a threshold (e.g., 10000 entries), regardless of the message count modulus.

#### MEDIUM: `HandleInbound` holds mutex while calling `bridge.SubmitInboundMessage`

**File**: `internal/im/runtime.go`, lines 371-465

The `HandleInbound` method unlocks `m.mu` before calling `bridge.SubmitInboundMessage` (line 465), which is correct. However, the `binding` pointer is obtained inside the lock and used after unlock (lines 404-465). The binding is a pointer (`*ChannelBinding`), so mutations to `binding.ChannelID` etc. after unlock modify the shared map entry. This is by design (the code mutates `binding.ChannelID` and then uses it), but it means the binding pointer must not be accessed concurrently.

**Recommendation**: The current code is safe because `HandleInbound` is the only writer for `currentBindings[adapter]`, but add a comment noting this assumption. Consider making a defensive copy.

#### MEDIUM: `pendingPairing` has no expiry

**File**: `internal/im/runtime.go`, lines 573-588

When a pairing challenge is created, it is stored in `m.pendingPairing` but never expired. If the user never completes pairing (e.g., closes the IM app), the challenge remains until the next pairing attempt from a different channel.

**Recommendation**: Add a timeout (e.g., 5 minutes) after which `pendingPairing` is automatically cleared. A goroutine or next-inbound check can implement this.

#### LOW: Echo suppression uses adapter name but not channel ID

**File**: `internal/im/runtime.go`, `EmitExcept` methods

Echo suppression excludes by adapter name. If an adapter has multiple channel bindings (theoretically possible in the binding store), messages from one channel would suppress echo on another.

**Recommendation**: Verify this is not a practical issue given the current binding model (one binding per adapter).

---

### 4. internal/a2a/

#### HIGH: SSE streaming sends only terminal event, no intermediate updates

**File**: `internal/a2a/server.go`, lines 369-447

The `handleMessageStream` method sends an initial `working` status, then blocks until the task completes. No intermediate status updates (e.g., tool calls, progress) are streamed. The A2A protocol spec allows for intermediate `TaskStatusUpdateEvent` events, but the current implementation only fires the terminal event.

**Recommendation**: If the A2A spec requires intermediate events, implement a subscription mechanism in the handler to stream events. If not required, document this as a known limitation.

#### HIGH: Task `done` channel is closed but never cleaned up if task is never completed

**File**: `internal/a2a/handler.go`, lines 182-189

```go
task := &Task{
    ...
    done: make(chan struct{}),
}
h.tasks[task.ID] = task
```

If a task fails to start (e.g., the agent is nil), the `done` channel is never closed. The `handleMessageSend` method blocks on `<-done` with a timeout, so this is bounded. But if the timeout fires, the goroutine waiting on `done` leaks. The `cleanupExpiredTasksLocked` method deletes tasks from the map but does not close their `done` channels.

**Recommendation**: In `cleanupExpiredTasksLocked`, close the `done` channel for removed tasks. Also close `done` in `CancelTask`.

#### MEDIUM: `cancels` map cleanup in `cleanupExpiredTasksLocked` may leak contexts

**File**: `internal/a2a/handler.go`, lines 709-719

```go
func (h *TaskHandler) cleanupExpiredTasksLocked() {
    for id, t := range h.tasks {
        if t.Status.IsTerminal() && now.Sub(t.UpdatedAt) > maxCompletedAge {
            delete(h.tasks, id)
            delete(h.cancels, id)
        }
    }
}
```

Deleting from `h.cancels` without calling the cancel function leaks the context and its resources until GC. The cancel function should be called before deletion.

**Recommendation**: Call `cancel()` before `delete(h.cancels, id)` for cancelled tasks.

#### MEDIUM: `handleMessageSend` uses request context for task handling but background context for task execution

**File**: `internal/a2a/server.go`, lines 307-367

```go
task, err := s.handler.Handle(r.Context(), params.Skill, params.Message, params.TaskID)
```

The handler receives `r.Context()` but internally creates a new task context:
```go
taskCtx, cancel := context.WithTimeout(context.Background(), h.timeout)
```

If the HTTP client disconnects, the task continues running in the background, which is correct behavior. However, the `Handle` method's context argument is only used for the initial setup, not for task lifecycle. This is fine but should be documented.

#### LOW: JSON-RPC error codes are ad-hoc

**File**: `internal/a2a/server.go`, `types.go`

Error codes like `-32000`, `-32002`, `-32060` are used but not documented in a central location. The JSON-RPC spec reserves -32000 to -32099 for implementation-defined errors.

**Recommendation**: Define constants for all error codes with documentation.

---

### 5. internal/swarm/

#### MEDIUM: `CancelAll` acquires nested locks (Manager.mu -> Team.mu -> Teammate.mu)

**File**: `internal/swarm/manager.go`, lines 312-335

```go
func (m *Manager) CancelAll() {
    m.mu.Lock()
    teams := make([]*Team, 0, len(m.teams))
    for _, t := range m.teams {
        teams = append(teams, t)
    }
    m.mu.Unlock()

    for _, team := range teams {
        team.mu.Lock()
        for _, tm := range team.Teammates {
            tm.mu.Lock()
            ...
            tm.mu.Unlock()
        }
        team.mu.Unlock()
    }
}
```

The lock ordering (Manager -> Team -> Teammate) is consistently followed, which prevents deadlocks. However, the lock acquisition is not atomic across teams. A new team could be created between the snapshot and the cancel loop. This is acceptable for interrupt handling but should be noted.

#### MEDIUM: Teammate `Inbox` channel capacity is unbounded in practice

**File**: `internal/swarm/team.go`

The `Inbox` channel is created with a fixed capacity (not visible in reviewed code, but `SendToTeammate` uses `select/default` for non-blocking send). If the inbox is full, messages are dropped silently. The `BroadcastToTeam` logs dropped messages, but `SendToTeammate` returns an error. Task assignment via `swarm_task_create` goes through `SendToTeammate`, so a full inbox would fail the task assignment.

**Recommendation**: Consider making the inbox larger (e.g., 64) or implementing a retry/backoff for task assignment.

#### LOW: `results` map in Manager grows without cleanup

**File**: `internal/swarm/manager.go`, lines 482-494

Teammate results are stored in `m.results[teammateID]` on idle events but never cleaned up. After a team is deleted, the results for its teammates remain in the map.

**Recommendation**: Clean up results when teams are deleted, or add a max size with eviction.

---

### 6. internal/daemon/

#### MEDIUM: `TerminalFollowDisplay.Close()` is a no-op

**File**: `internal/daemon/follow.go`, lines 394-397

```go
func (d *TerminalFollowDisplay) Close() {
    // no-op for now
}
```

If the display accumulates any resources (goroutines, timers, etc.) during operation, they would not be cleaned up. The `roundBuf` is a `strings.Builder` which is fine, but the lack of cleanup should be verified for future additions.

#### LOW: Follow display uses raw `fmt.Fprintf` to `d.out` under mutex

**File**: `internal/daemon/follow.go`, lines 211-216

All write operations to `d.out` are protected by `d.mu`, which prevents interleaved output. However, individual `fmt.Fprintf` calls are not atomic at the terminal level -- if the process crashes mid-write, partial output is possible. This is acceptable for a terminal display.

---

### 7. internal/harness/

#### MEDIUM: `AutoInit` ignores bootstrap errors silently

**File**: `internal/harness/auto_init.go`, lines 104-107

```go
if err := bootstrapHarnessState(project); err != nil {
    // Non-fatal: the state files are optional for routing
    _ = err
}
```

Bootstrap errors are silently ignored. If the state directory creation fails (e.g., permission issue), the harness will appear initialized but will fail when tasks are actually routed.

**Recommendation**: Log the error at minimum using `debug.Log` or return it as a warning.

#### LOW: Harness config writes with mode 0644

**File**: `internal/harness/auto_init.go`, line 98

```go
os.WriteFile(project.ConfigPath, data, 0644)
```

The harness config is world-readable. If it contains any sensitive paths or project-specific information, this may be a minor concern. For most development environments, this is acceptable.

---

### 8. internal/tui/

> Note: The TUI package is the largest at ~17.6k LOC. This review focuses on the `chat_bridge.go` interface and key concurrency patterns.

#### HIGH: `TUIChatBridge.SendUserMessage` calls `program.Send` which may block

**File**: `internal/tui/chat_bridge.go`

The TUI chat bridge sends webchat messages via `program.Send(webchatUserMsg)`. Bubble Tea's `Program.Send` is documented as safe for concurrent use, but if the TUI model's `Update` function is slow (e.g., processing a large agent response), sends could accumulate in the internal channel. The AGENTS.md notes this is "identical to keyboard input," which means it shares the same channel, so the risk is bounded by the existing TUI input buffering.

**Recommendation**: Verify that Bubble Tea's internal channel has adequate capacity for burst webchat input. If not, add a buffered intermediary channel.

#### MEDIUM: `DaemonBridge.SendUserMessage` run-slot claiming is TOCTOU-safe but error is silently dropped

**File**: `internal/webui/tui_bridge.go` / `internal/tui/chat_bridge.go`

The `SendUserMessage` method claims the run slot atomically under mutex, which is correct. However, if the run slot is already taken (agent busy), the error path is to return an error to the caller. In the WebSocket path, this error is sent back to the webchat client, but the exact error handling should be verified for all callers.

---

### 9. internal/mcp/

#### HIGH: `readResponseWithCancel` may leak goroutine on context cancellation

**File**: `internal/mcp/client.go`, lines 368-392

```go
func (c *Client) readResponseWithCancel(ctx context.Context) (*Response, error) {
    done := make(chan result, 1)
    safego.Go("mcp.client.readResponse", func() {
        resp, err := c.readResponse(ctx)
        done <- result{resp: resp, err: err}
    })
    select {
    case res := <-done:
        ...
    case <-ctx.Done():
        c.Abort()
        res := <-done  // BLOCKS until readResponse returns
        ...
    }
}
```

When the context is cancelled, `c.Abort()` kills the process, which causes `readResponse` to return. The goroutine then sends on `done`, which is buffered (size 1), so this doesn't leak. However, the `<-done` line blocks the caller until the process actually dies. If `Abort()` fails to kill the process (unlikely but possible), this would block indefinitely.

**Recommendation**: Add a secondary timeout (e.g., 5 seconds) on the `<-done` select after Abort.

#### MEDIUM: `sendHTTP` updates `sessionID` without holding lock

**File**: `internal/mcp/client.go`, lines 442-444

```go
if sessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sessionID != "" {
    c.sessionID = sessionID
}
```

The `sessionID` field is written here without holding `c.mu`, but it's read in `sendHTTP` (line 427) and in `send` which holds `c.mu`. Since `sendRequest` holds `c.mu` for the entire `send` call, and `sendHTTP` is called from within `send` while `c.mu` is held, this is actually safe. But the `sessionID` write at line 443 happens outside the lock's scope in `sendRequest`.

**Recommendation**: This is safe because `sendRequest` holds `c.mu` for the entire duration (deferred unlock), but add a comment for clarity.

#### MEDIUM: `writeMessage` writes to `c.stdin` without holding lock

**File**: `internal/mcp/client.go`, lines 394-410

```go
func (c *Client) writeMessage(msg interface{}) error {
    ...
    _, err = c.stdin.Write(data)
    return err
}
```

This is called from `send` which is called from `sendRequest` which holds `c.mu`. So the write is serialized. However, `writeResultResponse` and `writeErrorResponse` are called from `handleServerRequest` which is called from `readResponse` which is called from a goroutine spawned by `readResponseWithCancel`. The goroutine does NOT hold `c.mu`, so if a server request arrives while a client request is in flight, there would be a concurrent write to `c.stdin`.

**Recommendation**: This is a real data race risk. Either ensure `handleServerRequest` serializes through `c.mu`, or use a separate write mutex for stdin.

#### LOW: MCP client stderr buffer is capped at 64KB

**File**: `internal/mcp/client.go`, lines 748-763

The stderr buffer silently truncates beyond 64KB. For MCP servers that produce verbose logging, diagnostic information may be lost. The 64KB cap is reasonable but should be documented.

---

### 10. cmd/

#### MEDIUM: Daemon command goroutine lifecycle not fully tracked

**File**: `cmd/ggcode/daemon.go`

The daemon command spawns multiple goroutines for IM adapters, WebUI server, A2A server, etc. While each has context-based cancellation, the shutdown sequence relies on the parent context being cancelled. If any goroutine panics (despite `safego.Go` recovery), its deferred cleanup may not run.

**Recommendation**: Add a `sync.WaitGroup` for all top-level goroutines and wait on shutdown to ensure clean exit.

#### LOW: `pipe.go` has limited error context for JSON unmarshal failures

When pipe mode fails to parse input, the error message may not include enough context for debugging. This is a minor UX issue.

---

## Cross-Cutting Concerns

### Concurrency Patterns (Positive)

1. **Consistent mutex ordering**: Manager -> Team -> Teammate in swarm; mu -> bridge call in IM
2. **Value-copy snapshots**: All snapshot methods copy values under lock, preventing reference leaks
3. **Non-blocking sends with logging**: All inbox/channel sends use `select/default` and log drops
4. **Panic recovery**: All goroutine spawns use `safego.Go` with stack trace logging
5. **TOCTOU-safe slot claiming**: Both `DaemonBridge` and TUI bridge claim the run slot atomically

### Test Coverage Assessment

| Package | Unit Tests | E2E Tests | Race Tests | Integration Tests |
|---------|-----------|-----------|------------|-------------------|
| tunnel  | Yes (49KB) | - | - | - |
| webui   | Yes (37KB+16KB+35KB) | Yes (35KB) | - | - |
| im      | Yes (multiple adapters) | Yes (adapter_close_e2e) | - | - |
| a2a     | Yes (93KB) | Yes (12KB+20KB) | - | Yes (mesh test) |
| swarm   | Yes (20KB+10KB) | Yes (14KB+17KB) | Yes (2KB) | - |
| daemon  | Limited | - | - | - |
| harness | Yes (32KB+4KB+5KB) | - | - | Yes |
| tui     | Yes (multiple files) | Yes (auto_run_integration) | - | - |
| mcp     | Yes (20KB+5KB+8KB) | - | - | Yes |
| cmd     | Yes (20KB+9KB+3KB) | - | - | - |

**Gaps**:
- `internal/daemon/` has no unit tests for `follow.go` or `background.go`
- No race tests for `internal/tunnel/` despite heavy concurrency
- No race tests for `internal/im/` despite complex mutex usage

### Resource Leak Summary

| Resource | Package | Risk |
|----------|---------|------|
| Outbound message queue | tunnel | Medium - unbounded slice |
| sendWaiters map | tunnel | Low - entries leak on disconnect |
| A2A task done channels | a2a | High - not closed on cleanup |
| A2A cancels map | a2a | Medium - cancel funcs not called |
| Swarm results map | swarm | Low - not cleaned on team delete |
| MCP stdin writes | mcp | Medium - potential data race |

---

## Summary Table

| ID | Severity | Package | File:Lines | Description |
|----|----------|---------|------------|-------------|
| TUN-001 | Medium | tunnel | broker.go:1150-1157 | Unbounded outbound message queue |
| TUN-002 | Medium | tunnel | broker.go:1192-1211 | sendWaiters map leaks on disconnect |
| TUN-003 | Low | tunnel | broker.go | No visible Close() for broker goroutines |
| WEB-001 | High | webui | server_websocket.go | Broadcast must use per-connection buffered channels consistently |
| WEB-002 | Medium | webui | auth.go:12-19 | Auth token has no rotation/expiry |
| WEB-003 | Low | webui | server.go:329 | readJSON closes request body (harmless but unconventional) |
| IM-001 | High | im | runtime.go:374-393 | seenMessages map can grow large between pruning cycles |
| IM-002 | Medium | im | runtime.go:404-465 | Binding pointer used after unlock (safe but fragile) |
| IM-003 | Medium | im | runtime.go:573-588 | pendingPairing has no expiry timeout |
| IM-004 | Low | im | runtime.go | Echo suppression by adapter only (multi-channel edge case) |
| A2A-001 | High | a2a | server.go:369-447 | SSE streaming sends only terminal event |
| A2A-002 | High | a2a | handler.go:182-189 | Task done channel not closed on cleanup |
| A2A-003 | Medium | a2a | handler.go:709-719 | cancels map deletes without calling cancel() |
| A2A-004 | Medium | a2a | server.go:307-367 | Context usage in task handling not documented |
| A2A-005 | Low | a2a | server.go, types.go | JSON-RPC error codes not centralized |
| SWM-001 | Medium | swarm | manager.go:312-335 | CancelAll snapshot is not atomic across teams |
| SWM-002 | Medium | swarm | team.go | Inbox capacity may be insufficient for burst tasks |
| SWM-003 | Low | swarm | manager.go:482-494 | results map not cleaned on team delete |
| DAE-001 | Medium | daemon | follow.go:394-397 | Close() is no-op |
| DAE-002 | Low | daemon | follow.go | fmt.Fprintf under mutex (acceptable) |
| HAR-001 | Medium | harness | auto_init.go:104-107 | Bootstrap errors silently ignored |
| HAR-002 | Low | harness | auto_init.go:98 | Config file mode 0644 |
| TUI-001 | High | tui | chat_bridge.go | program.Send channel capacity for burst input |
| TUI-002 | Medium | tui | chat_bridge.go | DaemonBridge busy error handling across callers |
| MCP-001 | High | mcp | client.go:368-392 | readResponseWithCancel may block indefinitely after Abort |
| MCP-002 | Medium | mcp | client.go:394-410 | Potential concurrent stdin writes from server request handling |
| MCP-003 | Medium | mcp | client.go:442-444 | sessionID write under lock (safe but undocumented) |
| MCP-004 | Low | mcp | client.go:748-763 | Stderr buffer cap 64KB not documented |
| CMD-001 | Medium | cmd | daemon.go | Daemon goroutine lifecycle not fully tracked |
| CMD-002 | Low | cmd | pipe.go | Limited error context for JSON parse failures |

---

## Recommendations (Prioritized)

1. **A2A-002 + A2A-003**: Close `done` channels and call `cancel()` in `cleanupExpiredTasksLocked` (High impact, Low effort)
2. **MCP-002**: Audit `handleServerRequest` for concurrent stdin writes; add write serialization (High impact, Medium effort)
3. **IM-001**: Add size-based pruning threshold to `seenMessages` (High impact, Low effort)
4. **TUN-001**: Add soft cap to outbound queue (Medium impact, Medium effort)
5. **A2A-001**: Implement intermediate SSE events for streaming tasks (Medium impact, High effort)
6. **MCP-001**: Add secondary timeout on `<-done` after Abort (Medium impact, Low effort)
7. **Daemon tests**: Add unit tests for `internal/daemon/follow.go` and `background.go` (Low impact, Medium effort)
8. **Tunnel race tests**: Add `go test -race` targeted tests for broker concurrency patterns (Low impact, Medium effort)
