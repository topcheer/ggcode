# 🔒 Deadlock & Blocking Risk Review

> Audited areas: TUI main thread (`internal/tui/`), Agent loop (`internal/agent/`), Sub-Agent manager (`internal/subagent/`), MCP client (`internal/mcp/`).
>
> Goal: identify deadlocks, goroutine leaks, and any operations that block the Bubble Tea `Update()`/`View()` goroutine long enough to degrade the user experience.

---

## 🔴 P0 — High (deadlock / permanent goroutine leak)

### H1. Approval handler: bare `<-resp` with no context guard — goroutine leaks forever on TUI exit

**File:** `internal/tui/repl.go:372-383`

```go
r.agent.SetApprovalHandler(func(toolName string, input string) permission.Decision {
    if r.program == nil {
        return permission.Deny
    }
    resp := make(chan permission.Decision, 1)
    r.program.Send(ApprovalMsg{
        ToolName: toolName,
        Input:    input,
        Response: resp,
    })
    return <-resp // ← bare channel read, no ctx.Done() guard
})
```

**Problem:** The agent goroutine blocks on `<-resp` waiting for the TUI user to respond. If the user closes the TUI (or the program exits unexpectedly), the context is cancelled, but the agent goroutine is stuck in `<-resp` and **never checks `ctx.Done()`**. The goroutine leaks permanently along with all associated resources (LLM connections, file handles).

**Contrast:** `requestAskUser` (repl.go:301-315) correctly uses `select { case <-resp: ... case <-ctx.Done(): ... }`.

**Fix:** Wrap `<-resp` in a `select` with `<-ctx.Done()`. This requires changing the `SetApprovalHandler` signature to accept a `context.Context` parameter:

```go
// agent.go
type ApprovalFunc func(ctx context.Context, toolName string, input string) permission.Decision

// repl.go
r.agent.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
    if r.program == nil {
        return permission.Deny
    }
    resp := make(chan permission.Decision, 1)
    r.program.Send(ApprovalMsg{ToolName: toolName, Input: input, Response: resp})
    select {
    case d := <-resp:
        return d
    case <-ctx.Done():
        return permission.Deny
    }
})
```

Caller chain to update: `agent.go:456` → `agent_tool.go:89` → wherever `onApproval` is invoked.

---

### H2. DiffConfirm handler: bare `<-resp` with no context guard — same as H1

**File:** `internal/tui/repl.go:287-299`

```go
func (r *REPL) requestDiffConfirm(filePath, diffText string) bool {
    if r.program == nil {
        return true // Non-interactive (pipe) mode: auto-approve
    }
    resp := make(chan bool, 1)
    r.program.Send(DiffConfirmMsg{
        FilePath: filePath,
        DiffText: diffText,
        Response: resp,
    })
    return <-resp // ← bare channel read, no ctx.Done() guard
}
```

**Problem:** Identical to H1. If the TUI exits while the agent is waiting for a diff confirmation, the agent goroutine blocks forever.

**Fix:** Change `DiffConfirmFunc` to accept `context.Context`:

```go
// agent.go
type DiffConfirmFunc func(ctx context.Context, filePath, diffText string) bool

// repl.go
func (r *REPL) requestDiffConfirm(ctx context.Context, filePath, diffText string) bool {
    if r.program == nil {
        return true
    }
    resp := make(chan bool, 1)
    r.program.Send(DiffConfirmMsg{FilePath: filePath, DiffText: diffText, Response: resp})
    select {
    case ok := <-resp:
        return ok
    case <-ctx.Done():
        return false
    }
}
```

Caller chain to update: `agent_tool.go:127-139` → wherever `diffFn` is invoked.

---

### H3. Checkpoint handler reads `m.session` without lock — data race

**File:** `internal/tui/repl.go:386-396`

```go
r.agent.SetCheckpointHandler(func(messages []provider.Message, tokenCount int) {
    ses := r.model.Session() // ← reads m.session with no lock
    if ses == nil || r.store == nil {
        return
    }
    if err := r.store.AppendCheckpoint(ses, messages, tokenCount); err != nil {
        debug.Log("repl", "checkpoint save failed: %v", err)
    }
})
```

**Problem:** `Session()` (model.go:396-398) returns `m.session` directly with no lock. The TUI main thread modifies the same `m.session` object under `sessionMutex` in `appendUserMessage` (submit.go:20-43). `AppendCheckpoint` mutates `ses.UpdatedAt` and calls `updateIndex(ses)`. This is a **data race** on the session object.

**Fix options (pick one):**
1. Acquire `sessionMutex` in the checkpoint handler (quick fix, but increases lock contention).
2. Schedule the checkpoint save on the TUI thread via `program.Send` (preferred — avoids cross-goroutine session access entirely).

Option 2 example:

```go
r.agent.SetCheckpointHandler(func(messages []provider.Message, tokenCount int) {
    if r.program == nil {
        return
    }
    // Deep-copy messages to avoid race
    msgs := make([]provider.Message, len(messages))
    copy(msgs, messages)
    r.program.Send(checkpointSaveMsg{Messages: msgs, TokenCount: tokenCount})
})
// Then handle checkpointSaveMsg in Update() on the TUI thread.
```

---

## 🟠 P1 — Medium (blocking TUI main thread, degrading UX)

### M1. File browser: synchronous recursive filesystem traversal in `Update()`

**Files:**
- `internal/tui/file_browser.go:75-88` — `toggleFileBrowser()` called from `Update()` on `ctrl+f`
- `internal/tui/file_browser.go:90-135` — `syncFileBrowser()` called on every navigation key
- `internal/tui/file_browser.go:157-206` — `buildFileBrowserEntries()` recursive `os.ReadDir`
- `internal/tui/preview_panel.go:33-68` — `buildPreviewPanelStateForPath()` does `os.ReadFile`

```go
func (m *Model) toggleFileBrowser() {
    // Called from Update() — blocks TUI
    root, err := os.Getwd()
    state := newFileBrowserState(root)
    m.fileBrowser = state
    m.syncFileBrowser(true) // ← synchronous recursive directory traversal
}

func (m *Model) syncFileBrowser(initial bool) {
    state.entries = buildFileBrowserEntries(state.rootPath, state.expanded, state.filter)
    // ...
    state.preview = buildPreviewPanelStateForPath(entry.path, 0) // ← os.ReadFile
}
```

**Problem:** Every time the user navigates the file browser (up/down keys) or opens it (ctrl+f), the TUI thread synchronously:
1. Recursively reads the entire directory tree (`os.ReadDir` per directory)
2. Reads the selected file's content (`os.ReadFile`)

In large projects this causes 10-100ms pauses per keypress. On slow filesystems (NFS, FUSE) it could be seconds.

**Fix:** Move file preview loading into a `tea.Cmd`. Show a placeholder first, update asynchronously:

```go
func (m *Model) syncFileBrowser(initial bool) {
    state.entries = buildFileBrowserEntries(...) // keep this sync (fast for single-level display)
    state.preview = &previewPanelState{DisplayPath: entry.path, AbsPath: entry.path, Error: "Loading..."}
    // Return a tea.Cmd to load the content asynchronously
    return m.loadPreviewAsync(entry.path)
}
```

---

### M2. `View()` calls `sidebarGitBranch()` — reads `.git/HEAD` every frame

**Files:**
- `internal/tui/view.go:256` — calls `sidebarGitBranch()` from `renderSidebarDetailRow`
- `internal/tui/view.go:741-751` — `sidebarGitBranch()` calls `gitBranchForDir()`
- `internal/tui/view.go:789-835` — `gitBranchForDir()` loops `os.Stat` + `os.ReadFile`

```go
func (m Model) sidebarGitBranch() string {
    cwd, err := os.Getwd()
    branch, err := gitBranchForDir(cwd) // ← os.Stat + os.ReadFile per frame
    return branch
}
```

**Problem:** `View()` is called every render frame. `sidebarGitBranch()` is invoked unconditionally (when sidebar is visible), reading `.git/HEAD` on every frame. Usually OS-cached (~μs), but on network filesystems or under heavy I/O can be slow.

**Fix:** Cache the branch name, refresh via a periodic `tea.Cmd` (e.g., every 5 seconds):

```go
type gitBranchRefreshMsg struct{ branch string }

func (m *Model) pollGitBranch() tea.Cmd {
    return tea.Tick(5*time.Second, func(_ time.Time) tea.Msg {
        branch, _ := gitBranchForDir(cachedWorkDir)
        return gitBranchRefreshMsg{branch: branch}
    })
}
```

---

### M3. `refreshHarnessPanel()` — multiple synchronous SQLite queries in `Update()`

**File:** `internal/tui/harness_panel.go:105-175`

```go
func (m *Model) refreshHarnessPanel() { // Called from handleHarnessPanelKey on 'r' press
    project, cfg, err := loadHarnessForTUI(workDir) // file I/O
    panel.doctor, err = harness.Doctor(project, cfg) // SQLite
    panel.monitor, err = harness.BuildMonitorReport(project, ...) // SQLite
    panel.contexts, err = harness.BuildContextReport(project, cfg) // SQLite
    panel.tasks, err = harness.ListTasks(project) // SQLite
    panel.inbox, err = harness.BuildOwnerInbox(project, cfg) // SQLite
    panel.review, err = harness.BuildReviewReport(project, ...) // SQLite
    panel.promote, err = harness.BuildPromoteReport(project, ...) // SQLite
    panel.release, err = harness.BuildReleaseReport(project, ...) // SQLite
    panel.rollouts, err = harness.ListRollouts(project) // SQLite
}
```

**Problem:** User presses 'r' in the harness panel → 8+ SQLite queries execute synchronously on the TUI thread. With a large harness project this can freeze the UI for hundreds of milliseconds.

**Fix:** Move all queries into a `tea.Cmd`:

```go
func (m *Model) refreshHarnessPanelAsync() tea.Cmd {
    return func() tea.Msg {
        // ... run all queries off-thread ...
        return harnessPanelRefreshedMsg{...results...}
    }
}
```

---

### M4. `appendUserMessage` holds `sessionMutex` during file I/O

**File:** `internal/tui/submit.go:19-43`

```go
func (m *Model) appendUserMessage(text string) {
    m.sessionMutex().Lock()
    defer m.sessionMutex().Unlock()
    // ...
    _ = store.AppendMessage(m.session, msg) // ← file write, inside lock
}
```

**Problem:** `AppendMessage` performs file I/O (JSONL append) while holding `sessionMutex`. If the checkpoint handler (H3) also acquires this lock, the agent goroutine blocks on file I/O. On slow disks this increases contention.

**Fix:** Narrow the lock scope — copy data under the lock, write outside:

```go
func (m *Model) appendUserMessage(text string) {
    var ses *session.Session
    var msg provider.Message
    func() {
        m.sessionMutex().Lock()
        defer m.sessionMutex().Unlock()
        ses = m.session
        msg = provider.Message{...}
        if store, ok := m.sessionStore.(*session.JSONLStore); ok {
            // copy what we need
        }
    }()
    // File I/O outside the lock
    if store != nil && ses != nil {
        _ = store.AppendMessage(ses, msg)
    }
}
```

---

### M5. Harness run log chunk read in `Update()`

**File:** `internal/tui/commands.go:1358-1359` (called from `model_update.go:791-803`)

```go
// In Update(), harnessRunResultMsg handler:
chunk, nextOffset := readHarnessRunLogChunk(path, offset) // ← os.Open + io.ReadAll
```

**Problem:** `readHarnessRunLogChunk` does synchronous file I/O (open, seek, readAll) on the TUI thread when a harness run completes. Usually small files, but still technically blocking.

**Fix:** Move into the `tea.Cmd` that produces `harnessRunResultMsg`, or use a separate async command.

---

### M6. `CompleteMention` does `os.ReadDir` in `Update()` for autocomplete

**File:** `internal/tui/commands.go:54-68`, `completion.go:148-200`

```go
// Called on every keystroke via updateAutoComplete()
matches := CompleteMention(prefix, workDir) // ← os.ReadDir
```

**Problem:** Single-level `os.ReadDir` on every keystroke. Usually fast (~μs), but could lag on slow filesystems.

**Fix:** Consider debouncing autocomplete or caching directory listings.

---

## 🟡 P2 — Low-Medium (data races, no user-visible impact but incorrect under concurrency)

### L1. SubAgent `RunningCount()` reads `sa.Status` without `sa.mu`

**File:** `internal/subagent/manager.go:209-213`

```go
func (m *Manager) RunningCount() int {
    m.mu.Lock()
    defer m.mu.Unlock()
    for _, sa := range m.agents {
        if sa.Status == StatusRunning { // ← reads without sa.mu
            count++
        }
    }
    return count
}
```

**Fix:** Lock `sa.mu` when reading `sa.Status`, or use `sa.snapshot()`.

---

### L2. SubAgent `Cancel()` and `Complete()` race on terminal status

**Files:** `internal/subagent/manager.go:219-237` vs `257-284`

**Problem:** `Cancel()` sets `StatusCancelled`, then the runner's context cancellation triggers `Complete()` which overwrites with `StatusFailed`. The final status depends on ordering.

**Fix:** Add a `sa.done` flag (or check current status) in `Complete()` — skip if already in a terminal state:

```go
func (sa *SubAgent) setStatus(status AgentStatus) {
    sa.mu.Lock()
    defer sa.mu.Unlock()
    if sa.Status == StatusCancelled || sa.Status == StatusCompleted || sa.Status == StatusFailed {
        return // already terminal
    }
    sa.Status = status
}
```

---

### L3. SubAgent `SetOnUpdate`/`SetOnComplete` write callbacks without lock

**File:** `internal/subagent/manager.go:317-319, 357-358`

```go
func (m *Manager) SetOnUpdate(fn func(*SubAgent)) {
    m.onUpdate = fn // ← no lock
}
```

**Fix:** Acquire `m.mu` when setting callbacks.

---

### L4. Agent `emitUsage`/`PermissionPolicy()`/`Provider()` use write lock for reads

**Files:**
- `internal/agent/agent.go:640-647` — `emitUsage` uses `a.mu.Lock()` to read `a.onUsage`
- `internal/agent/agent.go:117-120` — `PermissionPolicy()` uses `a.mu.Lock()` to return value
- `internal/agent/agent.go:176-180` — `Provider()` uses `a.mu.Lock()` to return value

**Fix:** Change to `a.mu.RLock()` in all three methods.

---

### L5. MCP Client holds `c.mu` for entire request-response cycle

**File:** `internal/mcp/client.go:284-286`

```go
func (c *Client) sendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
    c.mu.Lock()
    defer c.mu.Unlock()
    // ... marshal, send, wait for response (could be 30+ seconds)
}
```

**Problem:** A single slow MCP server blocks all operations on that server (including `Close()` and notifications).

**Fix (complex, long-term):** Separate the send lock from the pending-response tracking. Use a map of request IDs to response channels protected by a separate mutex.

---

## 🟢 P3 — Low (minor, no immediate user impact)

| ID | File | Description |
|----|------|-------------|
| L6 | `internal/tui/view.go:770-787` | `shortenSidebarPath` calls `os.UserHomeDir()` every frame in `View()` |
| L7 | `internal/tui/mcp_panel.go:247` | `SetMCPDisabled` does file I/O on TUI thread (toggle MCP server enabled) |
| L8 | `internal/tui/model_update.go:207-211` | `ctrl+r` → `SaveSidebarPreference` does ReadFile+WriteFile in `Update()` |
| L9 | `internal/subagent/runner.go:65` | `sa.StartedAt` written in runner goroutine without `sa.mu` (redundant with `SetCancel`) |
| L10 | `internal/subagent/manager.go:167` | `Mailbox` channel (cap 16) is never consumed — messages silently dropped after 16 |
| L11 | `internal/agent/agent.go:399-447` | Autopilot + `maxIter=0` (unlimited) can loop unboundedly if tool calls reset the idle streak counter |

---

## Summary Table

| ID | Severity | Subsystem | File(s) | One-line description |
|----|----------|-----------|---------|---------------------|
| H1 | 🔴 P0 | TUI↔Agent | `repl.go:372-383` | Approval `<-resp` without ctx — goroutine leak on exit |
| H2 | 🔴 P0 | TUI↔Agent | `repl.go:287-299` | DiffConfirm `<-resp` without ctx — goroutine leak on exit |
| H3 | 🔴 P0 | TUI↔Agent | `repl.go:386-396` | Checkpoint handler reads session without lock — data race |
| M1 | 🟠 P1 | TUI | `file_browser.go:75-135` | Recursive `os.ReadDir` + `os.ReadFile` in `Update()` |
| M2 | 🟠 P1 | TUI | `view.go:741-835` | `.git/HEAD` read every frame in `View()` |
| M3 | 🟠 P1 | TUI | `harness_panel.go:105-175` | 8+ SQLite queries sync in `Update()` on 'r' press |
| M4 | 🟠 P1 | TUI | `submit.go:19-43` | `sessionMutex` held during file I/O |
| M5 | 🟠 P1 | TUI | `commands.go:1358` | Harness log read in `Update()` |
| M6 | 🟠 P1 | TUI | `commands.go:57` | `os.ReadDir` on every keystroke for autocomplete |
| L1 | 🟡 P2 | SubAgent | `manager.go:209-213` | `RunningCount` reads Status without `sa.mu` |
| L2 | 🟡 P2 | SubAgent | `manager.go:219-284` | Cancel/Complete race on terminal status |
| L3 | 🟡 P2 | SubAgent | `manager.go:317-358` | Callback fields written without lock |
| L4 | 🟡 P2 | Agent | `agent.go:117-180,640` | Write lock used for read-only operations |
| L5 | 🟡 P2 | MCP | `client.go:284-286` | Mutex held for entire request-response cycle |
