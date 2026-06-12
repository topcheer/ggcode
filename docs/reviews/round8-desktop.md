# Round 8 Desktop Review

**Scope**: `desktop/ggcode-desktop/`, `ggcode-relay/`, `desktop/markdownx/`
**Reviewer**: desktop (teammate)
**Date**: 2025-05-28

---

## Summary

The desktop application is a Fyne-based GUI that wraps the core ggcode agent. It includes an agent bridge for lifecycle management, IM integration via daemon bridge, WebSocket relay for mobile tunnel, and a custom Markdown rendering widget. The relay server is a standalone binary providing WebSocket relay with SQLite persistence.

Overall the codebase demonstrates competent engineering: proper `fyne.Do()` usage for thread safety, SQLite WAL mode with serialized connections, and reasonable lifecycle management. However, several security and robustness issues warrant attention.

---

## Critical

### C1. Relay WebSocket: No Authentication — Fully Open Relay
**File**: `ggcode-relay/relay.go:609-631`

```go
var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    ...
    token := r.URL.Query().Get("token")
    role := r.URL.Query().Get("role")
```

The relay server accepts WebSocket connections with **zero authentication**. The `token` parameter is a workspace-derived identifier, not a secret — any client knowing or guessing a token can connect, read all session events, inject messages, and impersonate server/client roles. The `CheckOrigin` callback always returns `true`, allowing cross-origin WebSocket connections from any domain.

**Impact**: On a publicly deployed relay, any attacker can:
- Read all relay events (session data, tool outputs, possibly sensitive information)
- Inject fake messages into sessions
- Impersonate the server and send `sharing_stopped` to disconnect clients
- Call `/nuke` endpoint (POST, no auth) to destroy all data

**Recommendation**: Add authentication middleware — at minimum an API key in the WebSocket handshake headers or query params. Consider HMAC-signed tokens with expiry. Restrict `CheckOrigin` to expected domains.

---

### C2. Relay `/nuke` Endpoint: Unauthenticated Data Destruction
**File**: `ggcode-relay/relay.go:738-761`

```go
mux.HandleFunc("/nuke", func(w http.ResponseWriter, r *http.Request) {
    if r.Method != "POST" {
        http.Error(w, "POST only", 405)
        return
    }
    if err := store.nukeAll(); err != nil {
```

The `/nuke` endpoint destroys all persisted data and disconnects all clients without any authentication check. Anyone who can reach the relay can issue `POST /nuke`.

**Recommendation**: Add at minimum a bearer token or shared secret check on admin endpoints. Consider binding admin endpoints to a separate listener or disabling in production.

---

## High

### H1. Predictable Temp File Paths — TOCTOU Race Condition
**File**: `desktop/ggcode-desktop/chat_view.go:189`, `desktop/ggcode-desktop/main.go:34`

```go
tmpFile := os.TempDir() + string(os.PathSeparator) + "ggcode-clipboard-paste.png"
os.Remove(tmpFile)
// ...
tmpIcon := filepath.Join(os.TempDir(), "ggcode-icon.png")
if err := os.WriteFile(tmpIcon, iconBytes, 0644); err == nil {
```

Both locations use hardcoded filenames in the system temp directory instead of `os.CreateTemp()`. This creates:
1. **TOCTOU race**: Between `os.Remove()` and `os.WriteFile()`, an attacker can create a symlink at the path
2. **Collision**: Multiple ggcode-desktop instances overwrite each other's temp files
3. **Permission leak**: `ggcode-icon.png` is written with `0644` (world-readable)

**Recommendation**: Use `os.CreateTemp()` with a unique prefix. For the dock icon, consider in-memory approaches or per-instance temp files.

---

### H2. Relay Token is Not Cryptographically Verified
**File**: `ggcode-relay/relay.go:617-624`

```go
token := r.URL.Query().Get("token")
if token == "" {
    conn.Close()
    return
}
```

The relay token is used directly as a room identifier without any validation. Tokens appear to be workspace paths or derivations thereof (e.g., `token-1234567890abcdef`). Any client that knows or can guess a workspace path can join the room. The token is passed in the URL query string, making it visible in logs, proxy logs, and browser history.

**Recommendation**: Use cryptographically random, time-limited tokens. Sign the token with HMAC so the relay can verify it was issued by a legitimate desktop instance.

---

### H3. No Rate Limiting on Relay WebSocket Connections
**File**: `ggcode-relay/relay.go:611-683`

There is no rate limiting on new WebSocket connections. An attacker can open thousands of connections to exhaust server resources. The `sendCh` buffer is set to 10,000 entries per peer (`relay.go:119`), meaning a single malicious connection can buffer significant memory.

**Recommendation**: Add connection rate limiting (per IP), maximum concurrent connections, and reduce sendCh buffer size to a more reasonable value (e.g., 256).

---

## Medium

### M1. Agent Bridge: Goroutine Leak in Pending Message Chain
**File**: `desktop/ggcode-desktop/agent_bridge.go:693-708`

```go
// Check for queued message from user while busy.
if pending, ok := b.drainPending(); ok {
    if pending.Hidden {
        _ = b.SendHiddenText(pending.Text)
    } else {
        ...
        _ = b.Send(pending.Text)
    }
}
```

When a pending message is drained and sent, `b.Send()` launches a new goroutine. If the user queues many messages rapidly, each `Send()` spawns a new goroutine that acquires `b.mu`. While this is bounded by user action, the chain is recursive in nature (each completion defers into a new `Send`). Under sustained rapid queuing, this could lead to many concurrent agent runs.

**Recommendation**: Add a maximum queue depth or serialize the pending chain instead of recursive goroutine spawning.

---

### M2. Markdownx: Unbounded HTTP Response Body Read
**File**: `desktop/markdownx/render.go:297-322`

```go
func fetchKroki(mermaidCode string) ([]byte, error) {
    client := &http.Client{Timeout: 15 * time.Second}
    resp, err := client.Post("https://kroki.io/mermaid/png", "text/plain", strings.NewReader(mermaidCode))
    ...
    return io.ReadAll(resp.Body)
}
```

`io.ReadAll` is used without size limits on external HTTP responses from `kroki.io` and `mermaid.ink`. A compromised or malicious backend could return extremely large responses, consuming memory.

**Recommendation**: Use `io.LimitReader(resp.Body, maxImageSize)` with a reasonable cap (e.g., 10MB).

---

### M3. SQLite Store: destroyRoom Runs in Fire-and-Forget Goroutine
**File**: `ggcode-relay/relay.go:541, 592`

```go
go func() { _ = h.store.destroyRoom(token) }()
```

Room destruction is performed in a fire-and-forget goroutine with errors silently discarded. While `SetMaxOpenConns(1)` serializes SQLite access, if the process exits before the goroutine completes, room data may not be fully cleaned up.

**Recommendation**: Use a buffered channel with a worker goroutine for serialized, trackable cleanup operations. Log errors.

---

### M4. Relay: `scheduleRoomExpiry` Reads `offlineTimer` Without Room Lock
**File**: `ggcode-relay/relay.go:501-513`

```go
func (h *hub) scheduleRoomExpiry(token string) {
    h.mu.RLock()
    r := h.rooms[token]
    h.mu.RUnlock()
    if r == nil {
        return
    }
    if r.offlineTimer != nil {
        r.offlineTimer.Stop()
    }
    r.offlineTimer = time.AfterFunc(5*time.Minute, func() {
```

The `offlineTimer` field is accessed and replaced without holding `r.mu`. If two goroutines call `scheduleRoomExpiry` concurrently for the same room (e.g., two clients disconnecting simultaneously), there's a data race on `r.offlineTimer`.

**Recommendation**: Protect `offlineTimer` access with `r.mu.Lock()`.

---

### M5. Chat View: `thinkingW` Field Accessed from Multiple Goroutines
**File**: `desktop/ggcode-desktop/chat_view.go:570-596`

```go
go func() {
    for cv.thinkingW != nil {
        time.Sleep(500 * time.Millisecond)
        if cv.thinkingW == nil {
            return
        }
        ...
    }
}()

func (cv *ChatView) hideThinking() {
    ...
    cv.thinkingW = nil
    ...
}
```

The animation goroutine reads `cv.thinkingW` without synchronization while `hideThinking()` writes it from the Fyne main thread. This is a data race, though unlikely to cause crashes due to the single-writer pattern.

**Recommendation**: Use `atomic.Value` or protect with a mutex. Alternatively, use `fyne.Do` to ensure all reads/writes happen on the main thread.

---

### M6. IM Bridge: No Timeout on QR Pairing HTTP Request
**File**: `desktop/ggcode-desktop/im_bridge.go:247-300`

The IM pairing bridge makes HTTP requests (QR code scanning, confirmation) with a default HTTP client (no explicit timeout). This can hang indefinitely if the IM server is unresponsive.

**Recommendation**: Use `http.Client{Timeout: 30 * time.Second}` for all IM bridge HTTP requests.

---

### M7. Relay: `readLoop` Double-Closes WebSocket Connection
**File**: `ggcode-relay/relay.go:137-163`

```go
func (p *peer) writeLoop() {
    defer p.conn.Close()
    ...
}

func (p *peer) readLoop(h *hub) {
    defer func() {
        close(p.done)
        p.conn.Close()  // second close
```

Both `writeLoop()` and `readLoop()` close the connection on exit. `writeLoop()` runs in a goroutine started by `readLoop()`, so when `readLoop` returns, it closes `p.done` which causes `writeLoop` to exit and also close the connection. The second `Close()` call is safe for `websocket.Conn` (it checks `atomic.CompareAndSwap`), but it's unnecessary and could mask errors.

**Recommendation**: Remove `p.conn.Close()` from `readLoop` since `writeLoop` already handles it.

---

## Low

### L1. Fyne Thread Safety Generally Well-Handled
**Files**: All `desktop/ggcode-desktop/*.go`

The codebase consistently uses `fyne.Do()` for UI mutations from background goroutines (37 call sites). Direct widget mutations (`SetText`, `Show`, `Hide`, `Refresh`) appear only in main-thread callbacks (button handlers, `SetOnClosed`, etc.) or within `fyne.Do()` blocks. The `safe_ui.go` module provides a clean event-driven architecture with `UIState` as a thread-safe intermediary. This is well done.

---

### L2. Dockerfile Uses Outdated Go Version
**File**: `ggcode-relay/Dockerfile:1`

```dockerfile
FROM golang:1.24-alpine AS builder
```

The project uses Go 1.26.1 (per `go.mod`) but the Dockerfile specifies `golang:1.24-alpine`. This version mismatch may cause build failures if Go 1.26 features are used.

**Recommendation**: Update to `golang:1.26-alpine`.

---

### L3. Relay: `sendCh` Buffer Size of 10,000
**File**: `ggcode-relay/relay.go:119`

```go
sendCh: make(chan []byte, 10000),
```

A 10,000-entry buffer per peer is extremely large. With many concurrent clients, this can consume significant memory. The AGENTS.md notes the codebase uses "blocking sends with write deadline" but the actual implementation uses a buffered channel (non-blocking enqueue), so backpressure isn't truly applied until the buffer is full.

**Recommendation**: Reduce to 256 or 512. If backpressure is desired, use blocking sends with a write deadline.

---

### L4. Markdownx: No Sanitization of Mermaid Code Before External Transmission
**File**: `desktop/markdownx/render.go:284-322`

User-provided Mermaid diagram code is sent verbatim to external services (`kroki.io`, `mermaid.ink`). While these are rendering services (not executing code), the user's diagram content is transmitted to third parties without disclosure.

**Recommendation**: Consider adding a configuration option to disable external rendering, or document the data transmission in a privacy notice.

---

### L5. Config File Written with 0600 but Temp Files Use 0644
**File**: `desktop/ggcode-desktop/config.go:67`, `desktop/ggcode-desktop/main.go:35`

Config saving correctly uses `0600` permissions, but the dock icon temp file uses `0644`. Inconsistent permission model.

**Recommendation**: Use `0600` consistently for all application temp files.

---

### L6. Relay: `io.ReadAll` on Kroki Response Without Size Limit
**File**: `desktop/markdownx/render.go:307, 321`

(Also noted in M2 above — duplicate concern in different component.) The response body from external Mermaid rendering services is read without size limits.

---

### L7. No Graceful Shutdown Signal Handling in Relay
**File**: `ggcode-relay/relay.go:709-785`

The relay server's `main()` function uses `log.Fatal(http.ListenAndServe(...))` without signal handling for graceful shutdown. On termination, in-flight WebSocket connections are dropped, and pending SQLite writes may be lost.

**Recommendation**: Use `http.Server.Shutdown()` with `context.WithTimeout` and signal handling.

---

### L8. Agent Bridge: `subAgentMgr` and `swarmMgr` Not Cleaned Up in `Close()`
**File**: `desktop/ggcode-desktop/agent_bridge.go:905-913`

```go
func (b *AgentBridge) Close() {
    b.Cancel()
    if b.metricCollector != nil {
        b.metricCollector.Stop()
    }
    if b.cronScheduler != nil {
        b.cronScheduler.Shutdown()
    }
}
```

The `Close()` method cancels the current agent run and stops metrics/cron, but does not call `subAgentMgr.CancelAll()` or `swarmMgr.CancelAll()`. Running sub-agents and swarm teammates may continue executing after window close.

**Recommendation**: Add cancellation for sub-agents and swarm teammates in `Close()`.

---

## Architecture Observations (Positive)

1. **Fyne goroutine model**: Consistent, correct use of `fyne.Do()` across 37+ sites. No main-thread violations detected.
2. **UIState event-driven pattern**: Clean separation between goroutine-safe state (`UIState` with `sync.Mutex`) and Fyne rendering.
3. **SQLite configuration**: WAL mode, `busy_timeout(5000)`, `SetMaxOpenConns(1)` — proper setup for concurrent access.
4. **Relay event deduplication**: History dedup by `sessionID+eventID` prevents bloat on reconnect.
5. **Agent bridge lifecycle**: Proper `context.WithCancel` with single-owner mutex locking pattern.
6. **Safe recover**: `safeRecover()` wrapper prevents panic crashes across critical initialization paths.

---

## Files Reviewed

| Component | Files | Lines |
|-----------|-------|-------|
| ggcode-desktop | `main.go`, `app.go`, `agent_bridge.go`, `chat_view.go`, `config.go`, `safe_ui.go`, `sidebar.go`, `im_bridge.go`, `im_window.go`, `file_tree.go`, `file_preview.go`, `metrics_window.go`, `log.go`, `types.go`, `theme.go`, `i18n.go` | ~7,800 |
| ggcode-relay | `relay.go`, `store.go`, `stats.go`, `trace.go`, `relay_test.go`, `store_test.go`, `trace_test.go` | ~2,500 |
| markdownx | `widget.go`, `parser.go`, `render.go`, `style.go`, `markdownx_test.go` | ~1,200 |

---

## Classification Summary

| Severity | Count | Key Issues |
|----------|-------|------------|
| Critical | 2 | Relay has no authentication; `/nuke` is unprotected |
| High | 3 | Predictable temp files; relay token not verified; no rate limiting |
| Medium | 7 | Goroutine chain leak; unbounded HTTP reads; SQLite fire-and-forget; data races; IM HTTP timeout |
| Low | 8 | Thread safety (positive); outdated Dockerfile; large send buffer; permissions inconsistency; no graceful shutdown |
