# Desktop GUI Application Review (Round 7)

**Scope:** `desktop/ggcode-desktop/` and `desktop/markdownx/`
**Framework:** Fyne v2 (Go)
**Date:** 2025-07-27

---

## Summary

The desktop app is a Fyne-based GUI for ggcode covering visual chat, IM integration, tool approval dialogs, tunnel relay for mobile, and an extended Markdown widget with Mermaid support. The codebase demonstrates strong awareness of Fyne's threading model (extensive use of `fyne.Do`), but has several areas of concern around resource cleanup, goroutine lifecycle, security, and cross-platform behavior.

**Files reviewed:** 26 Go source files (~15k LOC), 6 test files (~4k LOC)

---

## Critical Findings

### C-01: IM Adapter Started With Background Context -- No Graceful Shutdown Path

**File:** `desktop/ggcode-desktop/im_bridge.go`, lines 111-116
**Severity:** Critical

```go
controller, err := im.StartCurrentBindingAdapter(context.Background(), a.cfg.IM, a.imManager)
```

The IM adapter is started with `context.Background()`, meaning it has no cancellation mechanism tied to the app lifecycle. The `stopIMAdapters()` method may exist, but the adapter's internal goroutines (polling loops, WebSocket connections) will continue running until process exit if `Stop()` is not called or fails.

`SetOnClosed` in `app.go:132` calls `a.agentBridge.Close()` and `a.closeTunnelGracefully()`, but **never calls `a.stopIMAdapters()`**. This means IM adapter goroutines leak on window close.

### C-02: Temp Icon File Written With World-Readable Permissions

**File:** `desktop/ggcode-desktop/main.go`, lines 34-37
**Severity:** Critical (Security)

```go
tmpIcon := filepath.Join(os.TempDir(), "ggcode-icon.png")
if err := os.WriteFile(tmpIcon, iconBytes, 0644); err == nil {
    setDockIconMac(tmpIcon)
}
```

The icon is written with mode 0644 (world-readable) to the system temp directory. While the content is an embedded icon (not sensitive), this pattern:
1. Never cleans up the temp file
2. Uses a predictable filename in a shared directory (symlink attack vector on multi-user systems)
3. Writes on every app launch without checking if the file already exists or is a symlink

---

## High Findings

### H-01: Unbounded ChatMsgs Growth -- No Eviction for Long Sessions

**File:** `desktop/ggcode-desktop/safe_ui.go`, lines 59, 152
**Severity:** High

`UIState.ChatMsgs` is a `[]ChatMessage` that grows without bound during a session. Each message holds string content and widget references. For long-running sessions with many tool calls, this can grow to tens of thousands of entries. There is no compaction, eviction, or archiving mechanism in the desktop app (unlike the CLI agent which has `agent_compact.go`).

When `rebuildFromMessages()` is called (session resume), all messages are iterated and rendered, creating Fyne widget objects for each. This can cause significant memory pressure and UI lag.

### H-02: Mermaid Diagram Fetch Spawns Untracked Goroutine

**File:** `desktop/markdownx/render.go`, lines 256-279
**Severity:** High

```go
go func() {
    pngData, err := fetchMermaidPNG(mermaidCode)
    ...
}()
```

Each Mermaid block spawns a goroutine that makes HTTP requests to external services (kroki.io, mermaid.ink). There is no:
- Context cancellation mechanism
- Deduplication if the same block is re-rendered during streaming
- Limit on concurrent outbound requests
- Timeout shorter than 15s per backend (30s total if both fail)

If the widget is destroyed (e.g., session switch), the goroutine continues running and will attempt `fyne.Do` on destroyed widgets.

### H-03: No `io.LimitReader` on HTTP Response Bodies

**File:** `desktop/markdownx/render.go`, lines 297-322
**Severity:** High

```go
return io.ReadAll(resp.Body)
```

Both `fetchKroki` and `fetchMermaidInk` use `io.ReadAll` without a size limit. A compromised or malfunctioning Mermaid service could return an arbitrarily large response, exhausting memory.

### H-04: IM QR Polling Uses Context.Background -- No Cancellation on Dialog Close

**File:** `desktop/ggcode-desktop/im_bridge.go`, lines 207+
**Severity:** High

The `pollWechatQRStatus` function uses `context.Background()` for polling. If the user closes the QR dialog window, the polling goroutine continues making HTTP requests until it reaches a terminal state or times out. There is no cancel mechanism tied to dialog lifecycle.

### H-05: File Paths Exposed in Tool Arguments in Chat UI

**File:** `desktop/ggcode-desktop/agent_bridge.go`, lines 1400-1410
**Severity:** High (Security)

The `extractToolPath` function extracts and returns file paths from tool arguments. These paths are displayed verbatim in the chat UI (tool call cards), exposing the full filesystem path structure. In shared-screen scenarios or when tunnel is active (mobile client), this leaks internal directory structure including usernames (`/Users/john/projects/...`).

### H-06: approvalRespCh and askUserRespCh Are Unbuffered -- Potential Deadlock

**File:** `desktop/ggcode-desktop/agent_bridge.go`, lines 79, 83
**Severity:** High

```go
approvalRespCh    chan permission.Decision
askUserRespCh     chan tool.AskUserResponse
```

These channels are unbuffered. If the agent loop tries to send a response while the UI goroutine is blocked (e.g., during a Fyne layout pass), the agent goroutine blocks indefinitely. This can cause the entire agent loop to stall, requiring a force-quit.

---

## Medium Findings

### M-01: Debounce Timer Not Stopped on Widget Destroy

**File:** `desktop/markdownx/widget.go`, lines 152-165
**Severity:** Medium

```go
func (w *MarkdownWidget) scheduleRebuild() {
    w.debounceMu.Lock()
    defer w.debounceMu.Unlock()
    if w.debounceTimer != nil {
        w.debounceTimer.Stop()
    }
    w.debounceTimer = time.AfterFunc(50*time.Millisecond, func() {
        fyne.Do(func() {
            w.streamingRebuild()
        })
    })
}
```

When the `MarkdownWidget` is destroyed (e.g., session switch), the debounce timer may still fire. The `mdRenderer.Destroy()` method is empty -- it does not stop the timer. This can cause `fyne.Do` callbacks on a destroyed widget.

### M-02: fetchModels Race Condition on `fetchingModels` Flag

**File:** `desktop/ggcode-desktop/sidebar.go`, lines 510-553
**Severity:** Medium

```go
func (s *Sidebar) fetchModels() {
    if s.fetchingModels {
        return
    }
    s.fetchingModels = true
    defer func() { s.fetchingModels = false }()
```

`fetchingModels` is accessed without synchronization. If `fetchModels` is called from two different UI callbacks (vendor change + endpoint change), the check-and-set is not atomic. A `sync.Mutex` or `atomic.Bool` should be used.

### M-03: `fetchContextModels` Goroutine Leaks on Sidebar Rebuild

**File:** `desktop/ggcode-desktop/sidebar.go`, lines 342-378
**Severity:** Medium

`fetchContextModels` spawns a goroutine with a 30-second timeout. If the sidebar is rebuilt (e.g., on provider switch) before the goroutine completes, it writes to stale widget references. The `fyne.Do` callback may operate on widgets that are no longer in the widget tree.

### M-04: No Limit on io.ReadAll in File Preview

**File:** `desktop/ggcode-desktop/file_preview.go`, line 203
**Severity:** Medium

```go
data, err := os.ReadFile(fp.filePath)
```

`os.ReadFile` reads the entire file into memory with no size limit. If the user previews a large binary file or log file, this can exhaust memory. There should be a size check before reading.

### M-05: Agent Bridge Does Not Close Sub-Agent/Swarm Managers on Close

**File:** `desktop/ggcode-desktop/agent_bridge.go`, lines 888-893
**Severity:** Medium

```go
func (b *AgentBridge) Close() {
    b.Cancel()
    if b.cronScheduler != nil {
        b.cronScheduler.Shutdown()
    }
}
```

`Close()` cancels the current agent loop and shuts down cron, but does not call `subAgentMgr.CancelAll()` or `swarmMgr.CancelAll()`. Running sub-agents and swarm teammates continue executing after the bridge is closed. This is inconsistent with the TUI/daemon shutdown behavior.

### M-06: setWindowIcon Writes to Shared Temp Path Without Cleanup

**File:** `desktop/ggcode-desktop/main.go`, lines 34-37
**Severity:** Medium

The temp icon file is never removed. On long-running systems, this accumulates as a stale file. More importantly, the function does not verify the file is a regular file before writing (TOCTOU race).

### M-07: Cross-Platform -- Darwin-Specific CGo Code May Cause Build Issues

**File:** `desktop/ggcode-desktop/titlebar_darwin.go`, lines 1-47
**Severity:** Medium

The macOS titlebar code uses CGo with Objective-C (`#cgo CFLAGS: -x objective-c`). This requires a C compiler and macOS SDK, which:
1. Breaks cross-compilation from Linux/Windows to macOS
2. Fails silently if `CGO_ENABLED=0` is set (common in CI)
3. Has no fallback for non-macOS builds that might reference `setDockIconMac`

### M-08: io.ReadAll Without Limit in fetchLatestReleaseTag

**File:** `desktop/ggcode-desktop/app.go`, lines ~620-640
**Severity:** Medium

The update checker reads the full GitHub API response without size limits. GitHub API responses can be large, especially if rate-limited responses include verbose error bodies.

### M-09: ChatView.stopCh Closed Without Guarantee of Single Close

**File:** `desktop/ggcode-desktop/app.go`, lines 955-958
**Severity:** Medium

```go
if a.chatViewRef != nil && a.chatViewRef.stopCh != nil {
    close(a.chatViewRef.stopCh)
    a.chatViewRef.stopCh = nil
```

If `startChat()` is called concurrently (e.g., rapid session switching), `stopCh` could be closed twice, causing a panic. The `startChat` function uses `defer safeRecover` but the `close` happens before the defer setup.

---

## Low Findings

### L-01: No Accessibility Labels on Custom Widgets

**File:** `desktop/ggcode-desktop/chat_view.go` (throughout)
**Severity:** Low

Custom chat message widgets (`messageRow`, tool cards, accordion sections) do not set `fyne.CanvasObject` accessibility properties. Screen readers cannot navigate the chat content. Fyne supports `Accessible()` interface but it is not implemented.

### L-02: Hardcoded Mermaid Backend URLs

**File:** `desktop/markdownx/render.go`, lines 285-322
**Severity:** Low

The Mermaid rendering backends (kroki.io, mermaid.ink) are hardcoded with no configuration option. Users behind corporate firewalls or in regions where these services are blocked cannot render diagrams.

### L-03: Debug Log Statements Left in Production Code

**File:** `desktop/ggcode-desktop/agent_bridge.go`, line 603
**Severity:** Low

```go
log.Printf("[agent-bridge] Send called: %q", userMsg)
```

This logs every user message (including potentially sensitive content) to stderr. The desktop app has a structured `logf` helper, but `log.Printf` is used inconsistently in agent_bridge.go.

### L-04: No Dark/Light Theme Switching for Mermaid Diagrams

**File:** `desktop/markdownx/render.go`, lines 243-282
**Severity:** Low

Mermaid diagrams are fetched as PNG images without theme parameters. When the app switches between dark/light mode, diagrams retain their original appearance. The kroki.io API supports a `theme` parameter that could be used.

### L-05: Unused `linkSeg` Function

**File:** `desktop/markdownx/style.go`, line 56
**Severity:** Low

```go
func linkSeg(text, url string) *widget.HyperlinkSegment {
    return &widget.HyperlinkSegment{Text: text} // URL set by caller
}
```

This function creates a hyperlink segment without setting the URL. The comment says "URL set by caller" but no caller actually sets it. This is dead code that could mislead contributors.

### L-06: MarkdownWidget StreamingRebuild Accesses vbox.Objects Without Lock

**File:** `desktop/markdownx/widget.go`, lines 119-150
**Severity:** Low

`streamingRebuild()` reads and writes `w.vbox.Objects` and `w.lastCount` without holding the mutex. While Fyne requires UI operations on the main goroutine (ensured by `fyne.Do`), the `w.mu` is used elsewhere for `buffer` access, creating an inconsistent locking pattern.

### L-07: OnEvent Callback Called Under Lock in notify()

**File:** `desktop/ggcode-desktop/safe_ui.go`, lines 81-85
**Severity:** Low

```go
func (u *UIState) notify(e UIEvent) {
    if u.OnEvent != nil {
        u.OnEvent(e)
    }
}
```

`notify()` is called while holding `ChatMu` (e.g., in `AppendChat` at line 154, just after `Unlock`). However, in `AppendAssistantText` at line 203, the lock is released before `maybeNotifyChunk` -> `notify`. The pattern is inconsistent -- some paths hold the lock during notification and some do not. If `OnEvent` ever acquires `ChatMu`, it will deadlock.

### L-08: No Platform-Specific Paste Implementation for Linux Wayland

**File:** `desktop/ggcode-desktop/chat_view.go`, lines ~180-200
**Severity:** Low

The image paste function uses `xclip` for Linux, which only works on X11. Wayland users (increasingly common on modern Linux desktops) cannot paste images. There is no `wl-paste` fallback.

### L-09: file_tree.go Does Not Follow Symlinks

**File:** `desktop/ggcode-desktop/file_tree.go`
**Severity:** Low

The file tree walker does not handle symlink loops. If the workspace contains circular symlinks, `filepath.Walk` will skip them silently, which is correct but not communicated to the user.

---

## Test Coverage Gaps

| Area | Files | Status |
|------|-------|--------|
| `agent_bridge.go` (2821 LOC) | `agent_bridge_tunnel_test.go` (310 LOC) | Only tunnel message formatting tested. No tests for Send, Cancel, Close, approval flow, ask_user flow, sub-agent/swarm lifecycle, session management. |
| `app.go` (1352 LOC) | None | No unit tests for app lifecycle, window management, session switching, IM integration, tunnel connection. |
| `chat_view.go` (2235 LOC) | `chat_view_test.go` (minimal) | No tests for message rendering, streaming updates, tool result updates, image paste, rebuild from history. |
| `im_bridge.go` / `im_window.go` | `im_window_test.go` (93 LOC) | Only QR encoding tested. No tests for IM message routing, adapter lifecycle, pairing flow. |
| `sidebar.go` (760 LOC) | None | No tests for session loading, model fetching, provider switching. |
| `file_preview.go` (653 LOC) | Covered in `ui_test.go` | Basic server tests exist. No tests for binary file handling, large file handling, error cases. |
| `markdownx/` (5 files, ~32k LOC) | `markdownx_test.go` (292 LOC) | Parser and theme tests exist. No tests for streaming (`AppendChunk` debounce), Mermaid fetch (mocked), widget lifecycle, large content. |

**Overall test coverage estimate:** ~8-12% of desktop code is covered by automated tests.

---

## Recommendations (Priority Order)

1. **Fix IM adapter shutdown** -- Add `a.stopIMAdapters()` to `SetOnClosed` callback in `app.go:132`. Pass a cancellable context to `StartCurrentBindingAdapter`.

2. **Fix temp file security** -- Use `os.CreateTemp` with a random suffix and 0600 permissions. Clean up in `SetOnClosed`.

3. **Add `io.LimitReader`** -- Wrap all `io.ReadAll` calls (Mermaid fetch, update checker) with `io.LimitReader(resp.Body, 10<<20)` (10MB max).

4. **Add context to Mermaid goroutines** -- Store a context.CancelFunc in the widget and cancel it in `mdRenderer.Destroy()`.

5. **Stop debounce timer in Destroy** -- Add cleanup to `mdRenderer.Destroy()`:
   ```go
   func (r *mdRenderer) Destroy() {
       r.widget.debounceMu.Lock()
       if r.widget.debounceTimer != nil {
           r.widget.debounceTimer.Stop()
       }
       r.widget.debounceMu.Unlock()
   }
   ```

6. **Add sub-agent/swarm cleanup to Close()** -- Call `subAgentMgr.CancelAll()` and `swarmMgr.CancelAll()` in `AgentBridge.Close()`.

7. **Sanitize file paths in UI** -- Show only the basename or a relative path in tool call cards, not the full absolute path. Apply this in `extractToolPath` or its callers.

8. **Buffer approval channels** -- Make `approvalRespCh` and `askUserRespCh` buffered (cap 1) to prevent deadlocks.

9. **Add `sync.Mutex` to `fetchModels`** -- Replace the boolean flag with proper synchronization.

10. **Add size check to file preview** -- Stat the file first; skip preview if > 1MB.

11. **Remove `log.Printf` from production paths** -- Use the structured `logf` helper consistently, and avoid logging user message content.

12. **Add accessibility labels** -- Implement `Accessible()` on custom chat widgets for screen reader support.
