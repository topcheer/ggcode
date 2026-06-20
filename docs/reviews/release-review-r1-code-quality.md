# Code Quality Review: Releases v1.3.69 – v1.3.74

**Reviewer:** code-quality agent  
**Date:** 2025-01-20  
**Scope:** Git diffs `vX.Y.Z-1..vX.Y.Z` for each release in the range  
**Methodology:** Manual diff review of Go source, TypeScript frontend, Dart mobile, shell scripts, and CI configs  
**Cross-referenced:** Prior review rounds (5-7) in `memory/`, `.ggcode/memory/` design notes, and project `AGENTS.md`  

**Note:** This review focuses exclusively on code changes introduced in v1.3.69-v1.3.74. Findings from prior rounds (e.g., relay authentication, Flutter stream leaks, Broker data races) are not re-reported unless the code changed in this release range. New security findings (Critical shell injection, High AppleScript injection) have not been identified in any prior review.

---

## Executive Summary

Overall code quality is solid with good architectural patterns (backend abstraction, mutex discipline, graceful degradation). Test coverage is reasonable for new packages. However, **one Critical and one High severity security finding** were identified in the v1.3.73 terminal tools: shell injection via `printf` in `iterm2WriteToTTY` (Critical), and unescaped session ID in AppleScript (High). Several Medium findings relate to incomplete sanitization, argument quoting on Windows elevation, and a behavior change in tunnel event handling. Most Low findings are design limitations or minor robustness improvements, several of which are confirmed intentional per project memory notes.

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High     | 1 |
| Medium   | 7 |
| Low      | 9 |

---

## v1.3.74 — Extpane Terminal Tabs

**Files reviewed:** `internal/tui/extpane/manager.go`, `iterm2.go`, `kitty.go`, `tmux.go`, `format.go`, `iterm2_other.go`, `internal/tui/update_subagent.go`

### Positive Observations
- Strong safety architecture: `creating` dedup map, `failed` permanent-fail map, `maxPanes` hard cap (10), and per-agent `selfSessionID` guard to prevent closing ggcode's own terminal tab.
- Clean `Backend` interface abstraction (tmux/iTerm2/Kitty) with consistent `CreateTab`/`CloseTab`/`Name()` contract.
- Mutex discipline is generally correct: file operations happen under the lock; backend subprocess calls happen outside the lock.
- Good test coverage: 153 lines of tests covering backend detection priority, nil-backend no-ops, formatting, UTF-8 truncation, idempotent shutdown.

### Finding 1 — Medium: tmux hook restoration is fragile and silently fails

**File:** `internal/tui/extpane/tmux.go`, `CreateTab` method

> **Context from `.ggcode/memory/tmux-extpane-after-new-window-fix.md`:** The hook suppression approach is confirmed as the correct solution after testing alternatives (detached mode broke rendering, `window-created` was the wrong hook name). The design rationale is sound. However, the *restoration* logic has implementation gaps.

The code attempts to save and restore the `after-new-window` tmux hook, but the save/restore logic has gaps:

```go
// Save (may return error, silently ignored):
savedHook, _ := runTmux(ctx, "show-hooks", "-g", "after-new-window")
savedHook = strings.TrimSpace(savedHook)
if savedHook != "" && !strings.HasPrefix(savedHook, "after-new-window") {
    // show-hooks returns "after-new-window -> ..." format; keep full line for restore
}

// ... unset and create window ...

// Restore:
if savedHook != "" {
    hookCmd := strings.SplitN(savedHook, " -> ", 2)
    if len(hookCmd) == 2 {
        _, _ = runTmux(ctx, "set-hook", "-g", "after-new-window", strings.TrimSpace(hookCmd[1]))
    }
}
```

**Issues:**
1. The empty `if` block does nothing — it's dead code.
2. If `show-hooks` output format differs from `after-new-window -> <cmd>`, the split fails silently and the user's hook is permanently lost.
3. The `set-hook -g -u after-new-window` (unset) runs unconditionally even if no hook existed, which is harmless but wasteful.

**Recommendation:** Use a stricter format match (regex or prefix check) and log a warning when the hook cannot be parsed for restoration.

---

### Finding 2 — Low: `sanitizeFilename` does not collapse repeated replacements

**File:** `internal/tui/extpane/manager.go`, `sanitizeFilename`

```go
func sanitizeFilename(s string) string {
    s = strings.ReplaceAll(s, " ", "-")
    s = strings.ReplaceAll(s, "/", "-")
    for _, c := range s {
        if c < ' ' || c > '~' {
            s = strings.ReplaceAll(s, string(c), "-")
        }
    }
    return s
}
```

The loop iterates over the string while modifying it (the `strings.ReplaceAll` creates a new string each iteration), and the `for _, c := range s` range over a modified `s` may skip or re-process characters. While this won't cause a crash (the string is local), it's O(n²) and could produce filenames with runs of hyphens. Not a security issue since the result is used only as a temp file name in a private temp directory, but should be simplified with a single-pass builder.

---

### Finding 3 — Low (informational): Behavior change — subagent completion no longer queued to main agent

**File:** `internal/tui/update_subagent.go`, `handleSubAgentDoneMsg`

> **Confirmed intentional per AGENTS.md and `.ggcode/memory/` notes:** The project memory explicitly documents this behavior: "Sub-agent/teammate completion does not interrupt busy main agent. When a sub-agent or teammate finishes while the main agent is busy, the completion is shown as a system message and follow strip update only — no `agentHint` is queued or injected into the main agent's conversation. Only when the main agent is idle does completion trigger a new agent loop." This is a deliberate design decision, not a regression.

```go
// Before (v1.3.73):
// Agent is busy — queue for processing after current run.
m.queuePendingSubmissionHidden(agentHint)

// After (v1.3.74):
// Agent is busy — don't inject the completion notification into the
// main agent's conversation. The system message and follow strip above
// are sufficient for the user to see the result.
```

The change is well-reasoned — injecting completions into a busy agent loop could cause unexpected tool-call continuations. The 1-minute follow-strip grace period (documented in AGENTS.md) ensures the user still sees results. No action needed beyond ensuring release notes mention this.

---

### Finding 4 — Low: Grace period goroutine not tracked via WaitGroup on CloseAll race

**File:** `internal/tui/extpane/manager.go`, `HandleDone`

```go
m.wg.Add(1)
go func() {
    defer m.wg.Done()
    select {
    case <-time.After(m.gracePeriod):
        m.closePane(agentID)
    case <-m.stopCh:
    }
}()
```

If `CloseAll` is called while multiple grace goroutines are sleeping, `CloseAll` closes `stopCh`, causing these goroutines to return immediately without calling `closePane`. The panes are then cleaned up by `CloseAll`'s own loop. This is correct, but the `m.wg.Wait()` in `CloseAll` will wait for all grace goroutines to exit via the `stopCh` path, which is fine since they return promptly. No bug, but worth noting the interaction.

---

## v1.3.73 — Terminal Tools (iTerm2, Kitty, Warp, Ghostty)

**Files reviewed:** `internal/tool/iterm2.go`, `iterm2_darwin.go`, `iterm2_other.go`, `kitty.go`, `kitty_impl.go`, `ghostty.go`, `ghostty_darwin.go`, `ghostty_linux.go`, `ghostty_other.go`, `warp.go`, `warp_darwin.go`, `warp_other.go`, `labels.go`

### Finding 5 — CRITICAL: Shell injection via `printf` in `iterm2WriteToTTY`

**File:** `internal/tool/iterm2_darwin.go`

```go
func (t *Iterm2Tool) iterm2WriteToTTY(sessionID, data string) error {
    // ... get TTY path from AppleScript ...
    
    cmd := exec.Command("sh", "-c", fmt.Sprintf("printf '%s' > %s", data, tty))
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("failed to write to tty: %w", err)
    }
    return nil
}
```

**The `data` parameter is interpolated directly into a shell command string.** This function is called from:

- `executeSendKey` — `data` is an escape sequence built from user-provided key/modifier input
- `executeBadge` — `data` is `fmt.Sprintf("\033]1337;SetBadgeFormat=%s\007", badgeText)` where `badgeText` comes directly from agent JSON input
- `executeProfile` — `data` contains the profile name from agent input
- `executeClear` — `data` is a fixed escape sequence (safe)
- `executeMark` — `data` is a fixed escape sequence (safe)

**Exploit scenario:** An agent (or malicious prompt) calls the `badge` action with `text` set to:
```
'; rm -rf /; echo '
```
The resulting shell command becomes:
```sh
printf ''; rm -rf /; echo '' > /dev/ttys000
```

This executes `rm -rf /` as the user.

Even without malicious intent, badge text containing single quotes (e.g., `O'Brien`) will cause the printf to malfunction.

**The `tty` parameter** comes from AppleScript output and should be `/dev/ttysNNN` format, but it is also unescaped — a compromised iTerm2 or manipulated AppleScript output could inject additional shell commands.

**Recommendation:** Replace the shell command with a direct file write using Go's `os.OpenFile`:
```go
f, err := os.OpenFile(tty, os.O_WRONLY, 0)
if err != nil {
    return fmt.Errorf("open tty: %w", err)
}
defer f.Close()
_, err = io.WriteString(f, data)
return err
```

This eliminates the shell entirely.

---

### Finding 6 — HIGH: Unescaped session ID in AppleScript

**File:** `internal/tool/iterm2_darwin.go`, `iterm2SessionLookup`

```go
func iterm2SessionLookup(sessionID string) string {
    if sessionID == "" {
        return ""
    }
    return fmt.Sprintf(`
    set theSession to missing value
    repeat with w in windows
        ...
                if (id of s) is "%s" then
        ...
    end repeat
    if theSession is missing value then error "Session not found: %s"`, sessionID, sessionID)
}
```

The `sessionID` is interpolated directly into AppleScript **without** calling `escapeAS()`. This is called from `executeFocus`, `executeClose`, `executeInput`, `executeSendKey`, `executeResize`, `executeGetText`, `executeSetTitle`, `executeProfile`, `executeBadge`, `iterm2WriteToTTY` — essentially every action that accepts a `session_id`.

A session ID containing `"` would break out of the AppleScript string context, potentially allowing arbitrary AppleScript execution (which can run shell commands via `do shell script`).

Note: `sessionID` typically comes from `list` output (numeric IDs), but an agent or crafted prompt could supply arbitrary values.

**Recommendation:** Apply `escapeAS(sessionID)` in both interpolation sites within `iterm2SessionLookup`.

---

### Finding 7 — Medium: `escapeAS` is incomplete (does not handle control characters)

**File:** `internal/tool/ghostty_darwin.go` (also used by iTerm2 via same package)

> **Context from `.ggcode/memory/ghostty-applescript-linux-dbus.md`:** The ghostty AppleScript integration went through three commit fixes to get the `set act to` → `perform action` syntax and resize target correct. The `escapeAS` function itself was not hardened during these fixes.

```go
func escapeAS(s string) string {
    s = strings.ReplaceAll(s, `\`, `\\`)
    s = strings.ReplaceAll(s, `"`, `\"`)
    return s
}
```

This function only escapes backslash and double-quote. It does **not** handle:
- Newlines (`\n`, `\r`) — can break AppleScript syntax
- Tabs (`\t`)
- Other control characters (< 0x20)

In contrast, the extpane package's `sanitizeAS` function (v1.3.74) properly handles all of these.

An agent providing a profile name or badge text with embedded newlines could break the AppleScript, causing unexpected behavior.

**Recommendation:** Unify on a single, more complete `escapeAS` implementation (the one in `extpane/sanitizeAS`) or import it from a shared package. The `.ggcode/memory/ghostty-applescript-linux-dbus.md` notes confirm the ghostty AppleScript integration is already fragile (3 commits to fix syntax); hardening the escape function will reduce future breakage.

---

### Finding 8 — Medium: Working directory interpolated into shell command via AppleScript `write text`

**File:** `internal/tool/iterm2_darwin.go`, `executeSplit`, `executeNewTab`, `executeNewWindow`

```go
// In executeSplit:
script = fmt.Sprintf(`
tell application "iTerm"
    ...
        tell newSession
            write text "cd %s && %s"
        end tell
    ...
end tell`, lookup, targetSpec, splitCmd, escapeAS(wd), escapeAS(command))
```

The `escapeAS(wd)` makes the string safe for AppleScript, but the resulting text is **typed into a shell**. If the working directory contains shell metacharacters (e.g., `;`, `$()`, backticks, `|`), they will be interpreted by the shell.

Example: `working_dir` = `$(whoami)` would execute `whoami` when the cd command runs.

While `command` is intended to be executed by design, `working_dir` should be treated as data, not code.

**Recommendation:** Quote the working directory in the shell command: `cd '%s' && %s` where `%s` has single-quotes escaped (replace `'` with `'\''`). Or use iTerm2's session `cd` command if available.

---

### Finding 9 — Low: Kitty `left`/`up` split direction silently ignored

**File:** `internal/tool/kitty_impl.go`, `executeSplit`

```go
if dir == "left" || dir == "up" {
    // Kitty always creates vsplit to the right and hsplit below.
    // For left/up, we note the limitation.
}
```

This is a no-op block. The split is always created in the default direction (right/bottom). The user gets a success message with a note, but the split is in the wrong direction. This is a known limitation of kitty's remote control, but it should be more clearly communicated — perhaps as a warning rather than a silent success.

---

### Finding 10 — Low: Ghostty Linux most actions return "not supported" without graceful degradation

**File:** `internal/tool/ghostty_linux.go`

> **Confirmed limitation per `.ggcode/memory/ghostty-applescript-linux-dbus.md`:** The memory notes explicitly document: "Linux Ghostty 只暴露 application-level GActions，widget-level 动作不在 DBus 上" (Linux Ghostty only exposes application-level GActions, widget-level actions are not on DBus). The memory also proposes xdotool as a future fallback. This is a known platform limitation, not a code defect.

Most actions (`executeFocus`, `executeClose`, `executeInput`, `executeSendKey`, `executeSelectTab`) return hard errors:
```go
return Result{IsError: true, Content: "focus is not supported on Linux via DBus..."}
```

While this is honest, it means the ghostty tool is largely non-functional on Linux. The tool is registered whenever `TERM_PROGRAM == "ghostty"`, so Linux Ghostty users will have a tool that fails on most actions. Consider hiding these actions from the tool's parameter schema on Linux, or not registering the tool at all on Linux.

---

## v1.3.72 — CLI Wizards, Update Detection, MCP/IM Wizards

**Files reviewed:** `cmd/ggcode/mcp_cmd.go`, `cmd/ggcode/im_cmd.go`, `internal/update/detect.go`, `internal/update/elevate_windows.go`, `internal/update/update.go`, `npm/lib/install.js`

### Positive Observations
- Good wizard UX design with confirmation step and sensible defaults
- `tryGetVersion` uses 5-second timeout and `strings.NewReader("")` for stdin isolation
- Platform-specific build tags are used correctly

### Finding 11 — Medium: `quoteArgs` in Windows elevation does not escape embedded double quotes

**File:** `internal/update/elevate_windows.go`

```go
func quoteArgs(args []string) string {
    var b []byte
    for i, a := range args {
        if i > 0 {
            b = append(b, ' ')
        }
        if needsQuote(a) {
            b = append(b, '"')
            b = append(a...)    // <-- embedded " not escaped
            b = append(b, '"')
        } else {
            b = append(a...)
        }
    }
    return string(b)
}
```

If an argument contains a double quote (e.g., `--filter "name=test"`), it will be passed through without escaping, breaking the quoting when the elevated process parses its command line.

**Recommendation:** Escape embedded double quotes as `\"` (Windows command-line convention) or use `syscall.EscapeArg()` which handles this correctly.

---

### Finding 12 — Medium: `tryGetVersion` executes binaries discovered at arbitrary paths

**File:** `internal/update/detect.go`

```go
func tryGetVersion(binaryPath string) string {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, binaryPath, "version")
    cmd.Stderr = nil
    cmd.Stdin = strings.NewReader("")
    out, err := cmd.Output()
    ...
}
```

`FindOtherInstalls` discovers ggcode binaries at known locations and then executes each one to get its version. If a malicious binary is placed at a known path (e.g., `/usr/local/bin/ggcode` or `~/.local/bin/ggcode`), it will be executed with the current user's privileges.

The 5-second timeout limits damage but doesn't prevent it. The `version` subcommand argument is passed, but a malicious binary would ignore it.

**Recommendation:** This is inherent to the feature's purpose. Add a note in the documentation that `FindOtherInstalls` executes discovered binaries. Consider checksum verification if the current binary's checksum is known.

---

### Finding 13 — Low: `splitCommand` in MCP wizard doesn't handle single quotes

**File:** `cmd/ggcode/mcp_cmd.go`

```go
func splitCommand(s string) []string {
    var parts []string
    var current strings.Builder
    inQuote := false
    ...
    for i := 0; i < len(s); i++ {
        c := s[i]
        switch {
        case c == '"':
            inQuote = !inQuote
        ...
        }
    }
    flush()
    return parts
}
```

This is an intentionally simple parser, but it only handles double quotes. Single-quoted strings (e.g., `grep 'hello world' file`) will be split incorrectly at the space inside the quotes. The comment acknowledges this ("This is intentionally simple — not a full shell parser").

Since this is interactive-only input from the user, the risk is limited to confusing behavior, not a security issue.

---

## v1.3.71 — Mobile Session / Relay Auth Simplification

**Files reviewed:** `ggcode-relay/share_auth.go`, `ggcode-relay/share_auth_test.go`, `internal/tunnel/share_protocol.go`, `internal/agentruntime/tunnel_host.go`, `cmd/ggcode/daemon.go`

### Positive Observations
- Clean removal of legacy v1/v2 protocol code — significant dead code elimination
- Test files updated to match the simplified protocol

### Finding 14 — Medium: Breaking change — legacy share protocol clients will fail

**File:** `ggcode-relay/share_auth.go`, `validateShareHandshake`

> **Cross-reference:** Prior rounds 5-7 (`.ggcode/memory/round5-full-review-findings.md` etc.) flagged relay authentication issues (SEC-001: relay had no authentication, SEC-002: CheckOrigin all-accept). The v1.3.71 protocol simplification addresses the v3 auth model but removes legacy fallback paths.

The v1.3.71 changes removed all backward compatibility with protocol versions < 3:

```go
// Before: handled legacy token path, v2 params, etc.
// After:
if roomID == "" {
    return nil, http.StatusBadRequest, "missing room_id"
}
if protocolVersion < requiredShareProtocolVersion {
    ...
}
```

Any client (older mobile app, older desktop, older CLI) connecting with the legacy `token` parameter instead of `room_id` will receive a `400 Bad Request: missing room_id` error. Previously, the server returned `410 Gone` with an upgrade message.

**Recommendation:** Ensure all client apps are updated before deploying this relay change. Consider keeping a transitional `410 Gone` response for legacy token-only connections during the rollout window.

---

### Finding 15 — Low: Unsubscribe leak in daemon `t` key tunnel toggle

**File:** `cmd/ggcode/daemon.go`, key handler for `t`

```go
unsubscribeTunnel := bridge.Subscribe(ctrl.HandleStreamEvent)
_ = unsubscribeTunnel // cleaned up on daemon exit
```

When the user presses `t` to toggle the tunnel on, a new event subscription is created but the unsubscribe function is discarded (`_ = unsubscribeTunnel`). If the user toggles the tunnel on and off multiple times, multiple subscriptions accumulate, causing duplicate event handling. This is acknowledged with a comment ("cleaned up on daemon exit") but could lead to performance issues during long daemon sessions.

**Recommendation:** Store the unsubscribe function and call it when the tunnel is stopped, or track it in a way that allows cleanup.

---

## v1.3.70 — Desktop Rendering, Tunnel Barrier, Swarm Board

**Files reviewed:** `internal/agentruntime/tunnel_host.go`, `internal/tunnel/broker.go`, `internal/swarm/manager.go`, `internal/swarm/team.go`, `desktop/ggcode-desktop-wails/frontend/src/components/chatStreamState.ts`, `desktop/ggcode-desktop-wails/frontend/src/components/chatStreamState.test.ts`

### Positive Observations
- Excellent extraction of chat stream state logic into pure, testable functions (`chatStreamState.ts`)
- 155 lines of focused unit tests for stream state mutations
- `TeamBoardSnapshot` properly uses read locks and makes defensive copies
- `activeSessionBarrier` correctly uses `flushAllText()` to ensure ordering

### Finding 16 — Medium: Removed `rollover` call may affect message boundary handling

**File:** `internal/agentruntime/tunnel_host.go`, `PushStreamEvent`

> **Context from `.ggcode/memory/tunnel-host-refactoring.md`:** The TunnelHost was unified across TUI/Daemon/Wails. The memory notes confirm: "Daemon: HandleStreamEvent delegates. rolloverMainStream/displayName/truncate 保留用于 replay" (rollover functions are retained for replay use). This suggests the removal from the text handler is intentional, with rollover retained for replay scenarios.

```go
case provider.StreamEventText:
    msgID := h.ensureMsgID(broker)
    // REMOVED: h.rollover(broker, false)
    h.markActive()
    broker.PushReasoningDone(TunnelReasoningMsgID(msgID))
    broker.PushText(msgID, ev.Text)
```

The `rollover` call was removed from the text event handler. If `rollover` was responsible for finalizing the previous message or splitting messages at turn boundaries, its removal could cause messages to merge or lose proper boundaries in the tunnel stream.

**Recommendation:** Verify that `PushText` with `ensureMsgID` handles all cases that `rollover` previously covered. If `rollover` is now dead code, remove it entirely to avoid confusion.

---

### Finding 17 — Low: `activeSessionBarrier` returns empty projection hash

**File:** `internal/tunnel/broker.go`

```go
func (b *Broker) activeSessionBarrier() (string, int64, string) {
    b.flushAllText()
    ordinal := b.nextEvent.Load()
    eventID := ""
    if ordinal > 0 {
        eventID = fmt.Sprintf("ev-%09d", ordinal)
    }
    return eventID, ordinal, ""  // <-- projectionHash is always empty
}
```

The `projectionHash` (third return value) is always `""`. This is passed to `SendActiveSession`/`SendActiveSessionWithMode` but provides no content integrity verification. If this is intended for future use, it should be documented. If the barrier is supposed to provide consistency guarantees, the empty hash weakens them.

---

### Finding 18 — Low: `chatStreamState.ts` reasoning append doesn't handle explicit ID mismatch correctly

**File:** `desktop/ggcode-desktop-wails/frontend/src/components/chatStreamState.ts`

```typescript
export function appendReasoningChunk<T extends StreamChatMessage>(
    messages: T[],
    content: string,
    nextID: () => string,
    now: () => number = () => Date.now(),
    explicitMessageID?: string,
): T[] {
    const out = [...messages]
    if (explicitMessageID) {
        const idx = out.findIndex(m => m.id === explicitMessageID)
        if (idx >= 0 && out[idx].role === 'reasoning') {
            out[idx] = { ...out[idx], content: out[idx].content + content, streaming: true }
            return out
        }
        // If found but wrong role, pushes a NEW message with the same ID — duplicate ID
        out.push({ id: explicitMessageID, role: 'reasoning', content, streaming: true, timestamp: now() } as T)
        return out
    }
    ...
}
```

If a message with `explicitMessageID` exists but has a different role (e.g., `assistant`), the code pushes a **new** message with the same ID. This creates a duplicate ID in the message list, which could cause React rendering issues or state lookup confusion.

**Recommendation:** Either skip the push and update the existing message's role, or generate a unique ID.

---

## v1.3.69 — Python Installer Cleanup

**Files reviewed:** `python/ggcode_release_installer/cli.py`, `python/tests/test_cli.py`

### Positive Observations
- Clean removal of dead code from the CLI
- Test added for the remaining functionality

No issues found in this release. The changes are minimal (11 lines removed from `cli.py`, 6 lines added to tests).

---

## Test Coverage Assessment

| Release | New Source (approx) | New Tests (approx) | Coverage Notes |
|---------|-------------------|-------------------|----------------|
| v1.3.69 | ~11 lines | ~6 lines | Adequate for scope |
| v1.3.70 | ~5000 lines | ~1500 lines | Good: chatStreamState, swarm, tunnel tests added |
| v1.3.71 | ~2600 lines | ~165 lines | Adequate: relay auth tests updated |
| v1.3.72 | ~1700 lines | ~89 lines | **Weak**: no tests for wizard input handling, `splitCommand`, `quoteArgs` |
| v1.3.73 | ~4500 lines | ~560 lines | **Adequate but incomplete**: tests cover detection/validation but NOT shell command construction or AppleScript generation |
| v1.3.74 | ~900 lines | ~153 lines | Good: extpane manager, formatting, UTF-8 |

### Missing Test Coverage (Priority)
1. **`iterm2WriteToTTY`** — no test verifies that the shell command is safely constructed (it isn't)
2. **`escapeAS`** — no test for control character handling
3. **`splitCommand`** — no test for quote edge cases
4. **`quoteArgs`** (Windows) — no test for embedded double quotes
5. **`iterm2SessionLookup`** — no test for special characters in session IDs

---

## TODO/FIXME Scan

No shipping TODOs or FIXMEs were found in the reviewed code changes. The only "TODO" references are in tool description strings (example text for the `description` parameter).

---

## Summary of Recommendations

### Immediate Action Required
1. **Fix `iterm2WriteToTTY`** (Critical) — Replace `exec.Command("sh", "-c", ...)` with direct `os.OpenFile` + `io.WriteString` to eliminate shell injection.
2. **Escape session IDs** (High) — Apply `escapeAS()` to `sessionID` in `iterm2SessionLookup`.

### Should Fix Before Next Release
3. Improve `escapeAS` to handle control characters (unify with `extpane/sanitizeAS`).
4. Quote working directory in shell commands (`cd '%s'`).
5. Fix `quoteArgs` on Windows to escape embedded double quotes.
6. Add tests for shell/AppleScript command construction.

### Track for Future
7. Verify `rollover` removal doesn't affect message boundaries.
8. Clean up tunnel toggle unsubscribe leak.
9. Improve tmux hook restoration parsing.
10. Subagent completion behavior change — confirmed intentional per AGENTS.md; no action needed.
11. Ghostty Linux limitations — confirmed platform constraint per memory notes; consider xdotool fallback.

---

## Prior Review Cross-Reference

Findings from prior review rounds that remain relevant to this release range:

| Prior Finding | Status | Relation to This Review |
|---|---|---|
| R5/R6: Relay no auth (SEC-001/002) | Resolved | v1.3.71 protocol simplification addresses auth; but legacy fallback removed (Finding 14) |
| R6: Broker data races | Fixed (8fb669c5) | Not re-reviewed; code changed in v1.3.70/v1.3.71 |
| R7: Sub-agent Manager lock ordering | Open | Not in scope of v1.3.69-74 diffs |
| R7: Flutter Stream subscription leak | Open | Not in scope (no Flutter changes in range) |
| R7: Desktop IM adapter goroutine leak | Open | Not in scope (no desktop IM changes in range) |

**New findings unique to v1.3.69-v1.3.74 (not in any prior review):**
- Finding 5 (Critical): Shell injection in `iterm2WriteToTTY`
- Finding 6 (High): Unescaped session ID in `iterm2SessionLookup`
- Findings 7-13: Terminal tool and update/wizard issues
- Findings 1-4: Extpane package issues (new code in v1.3.74)
