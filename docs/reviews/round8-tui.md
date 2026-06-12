# Round 8 TUI Review

**Reviewer**: tui (tm-4)
**Date**: 2025-05-28
**Scope**: `internal/tui/` (47+ files, ~17.6k LOC), `internal/commands/`

---

## Summary

The TUI layer is mature and well-structured. The codebase demonstrates strong awareness of Bubble Tea's value-copy semantics, goroutine safety patterns, and event-loop architecture. The i18n system is comprehensive with only minor gaps. The most significant risks are in i18n catalog drift (13 missing zh-CN keys for IM slash commands) and duplicated done-handling logic between `doneMsg` and `agentDoneMsg`.

---

## Findings

### CRITICAL

None found.

### HIGH

#### H-1: i18n catalog drift -- 13 slash command keys missing in zh-CN

**File**: `internal/tui/i18n_zh.go`
**Keys missing**: `slash.impersonate`, `slash.irc`, `slash.knight`, `slash.matrix`, `slash.mattermost`, `slash.nostr`, `slash.signal`, `slash.stream`, `slash.twitch`, `slash.wechat`, `slash.wecom`, `slash.whatsapp`, `agents.unavailable`

**Impact**: Chinese-locale users see raw English key strings (e.g. `slash.knight`) in autocomplete suggestions, slash command help, and error messages for these commands. This is user-facing broken text.

**Evidence**: EN catalog has 627 keys vs ZH catalog's 616 keys. The 13 missing keys are all slash-command related, covering most non-primary IM adapters and the impersonate/knight/stream features.

**Fix**: Add the 13 missing `case` entries to `zhCatalog()` with appropriate Chinese translations. Also note that `pairing.blacklisted` and `pairing.rejected` exist only in ZH but not EN -- these should be added to EN or confirmed as dead code.

#### H-2: Duplicated state-transition logic in doneMsg vs agentDoneMsg handlers

**File**: `internal/tui/update_done.go`

**Problem**: `handleDoneMsg()` and `handleAgentDoneMsg()` contain nearly identical state-reset logic (lines 12-43 vs 48-80). Both:
1. Set `m.loading = false`
2. Reset `m.spinner.Stop()`
3. Call `m.chatFinishAllRunningTools()`
4. Null `m.cancelFunc`
5. Call `m.chatFinishAssistant()`
6. Reset `wasCanceled`/`wasFailed` flags
7. Clear status fields
8. Flush stream buffer
9. Process pending submissions

However, `handleDoneMsg` additionally calls `m.pendingIMStreamText()` and `m.emitIMText()`, while `handleAgentDoneMsg` additionally reads `m.projMemFiles`. This duplication risks the two paths diverging during maintenance -- if one is updated, the other may be missed.

**Impact**: If a new state field is added that needs resetting on agent completion, forgetting to update one path leads to stale state. Currently `handleDoneMsg` emits final IM text while `handleAgentDoneMsg` does not -- this appears intentional (legacy vs agent path), but is not documented.

**Fix**: Extract a shared `resetAgentRunState()` method and have both handlers call it, then add only their unique post-reset logic.

#### H-3: m.program.Send from goroutines without nil-check consistency

**Files**: `internal/tui/submit.go`, `internal/tui/tunnel.go`, `internal/tui/shell_mode.go`, `internal/tui/model.go`, `internal/tui/commands_harness_task.go`

**Problem**: `m.program.Send()` is called from goroutines spawned via `safego.Go()` or closures. While most call sites check `m.program != nil` before sending, there is a TOCTOU window: the program could theoretically be set to nil between the nil check and the Send call. Additionally, `program.Send()` in bubbletea v2 is documented as goroutine-safe, so this is not a correctness issue, but the inconsistent nil-checking pattern suggests the code was written with an assumption that `program.Send` could panic on nil receiver.

**Affected call sites** (30+):
- `submit.go:98,105,136,147,152,251,298,313,325,362,373,428,438,442,444` -- all within safego.Go closures
- `tunnel.go:251,607,616,625,634,643,652,661` -- broker callbacks
- `shell_mode.go:155,168,178,184` -- command polling goroutine
- `model.go:650,904,909` -- IM/Knight event hooks
- `commands_harness_task.go:57,105,134,643` -- harness goroutines

**Impact**: Low practical risk since `program.Send()` is goroutine-safe and the program is set early and never cleared. However, the defensive nil-check pattern should be applied consistently or removed entirely with a documented assumption.

**Fix**: Either (a) add a comment in Model struct that `program` is set once and never nil during agent runs, or (b) extract a `sendMsg(msg)` helper that does the nil check centrally.

### MEDIUM

#### M-1: handleAgentDoneMsg missing IM emit for final text

**File**: `internal/tui/update_done.go`, lines 48-80

**Problem**: `handleDoneMsg` (line 13-14) calls `m.pendingIMStreamText()` and emits it via `m.emitIMText()`, but `handleAgentDoneMsg` does neither. This means when the agent path is used (which is the primary path for normal agent runs), the final IM streaming text may not be emitted if the batch wasn't already flushed.

**Impact**: IM channels (Telegram, Discord, etc.) may miss the final portion of the agent's response when the agent completes normally through the `agentDoneMsg` path.

**Fix**: Add `finalIMText := m.pendingIMStreamText()` and `m.emitIMText(finalIMText)` to `handleAgentDoneMsg`, mirroring `handleDoneMsg`.

#### M-2: closeActivePanel dual-close for wechat/wecom panels

**File**: `internal/tui/model.go`, lines 736-741

```go
case m.wechatPanel != nil:
    m.closeWechatPanel()
    m.closeIMPanel()  // second close
case m.wecomPanel != nil:
    m.closeWeComPanel()
    m.closeIMPanel()  // second close
```

**Problem**: WeChat and WeCom panels call both their specific close AND `closeIMPanel()`. If the IM panel is already nil (closed independently), `closeIMPanel()` may operate on a nil panel. The other IM adapter panels (TG, QQ, Discord, etc.) only call their own close.

**Impact**: If the IM panel was already closed, `closeIMPanel()` may be a no-op (if it checks for nil) or may panic (if it doesn't). This creates an asymmetry in panel lifecycle management.

**Fix**: Verify `closeIMPanel()` guards against nil, and document why WeChat/WeCom need dual close but other adapters don't.

#### M-3: No memory cleanup for panel viewports on close

**Files**: `internal/tui/preview_panel.go`, `internal/tui/stream_panel.go`, `internal/tui/inspector_panel.go`, `internal/tui/harness_panel.go`

**Problem**: When panels are closed (e.g. `closePreviewPanel()`, `closeStreamPanel()`), the panel struct is set to nil on the Model but the viewport's internal buffers (lipgloss rendering cache, string builders) are not explicitly released. Go's GC will eventually reclaim these, but during rapid panel switching, accumulated unreleased viewports may cause transient memory pressure.

**Impact**: Negligible in practice due to GC, but could be noticeable with large file preview buffers in the fullscreen browser or inspector with large conversation histories.

**Fix**: Consider adding a `Close()` or `Reset()` method to panels that clears viewport content before nil-ing the panel reference.

#### M-4: Resize handling misses panel viewport resizing during active agent run

**File**: `internal/tui/resize.go`

```go
func (m *Model) handleResize(width, height int) {
    // ...
    if m.chatList != nil {
        m.chatList.SetSize(m.conversationInnerWidth(), conversationInnerHeight(m.conversationPanelHeight()))
    }
}
```

**Problem**: `handleResize` resizes the main chat list but does not propagate size changes to open panel viewports (inspector, stats, harness, stream, preview, file browser). Panels opened before a resize will render at their original size until the next `View()` call that triggers a re-layout.

**Impact**: Panel content may appear clipped or misaligned after terminal resize if the panel uses a viewport that was sized at open time. The main chat area handles resize correctly.

**Fix**: Add viewport resizing for all open panels in `handleResize()`.

#### M-5: Stream batch goroutine lifecycle not tied to agent context

**File**: `internal/tui/submit.go`, lines ~200-260 (batch ticker goroutine)

**Problem**: The stream batch ticker goroutine is started in `runAgentWithContent` and stopped via `close(batchDone)`. However, if the TUI model is destroyed (e.g. program exit) while the agent goroutine is still running, the batch ticker may attempt to call `m.program.Send()` on a nil/closed program. The `safego.Go` wrapper provides panic recovery, but the goroutine could leak briefly.

**Impact**: Minor -- the goroutine will eventually exit when the batch channel is closed by the deferred cleanup in the agent goroutine.

**Fix**: Consider passing the context to the ticker goroutine and checking `ctx.Done()` in the ticker loop.

#### M-6: pendingQueue uses pointer-to-struct correctly but Model value-copy still risks stale reads

**File**: `internal/tui/model.go`, lines 265-273

**Problem**: `pendingQueue` is stored as `*pendingQueue` to avoid value-copy splitting, which is correct. However, other state fields like `cancelFunc`, `loading`, `runCanceled`, `activeAgentRunID` are plain value fields that are mutated both in Update methods AND in goroutine closures (e.g. `submit.go:89` modifies `m.loading`, `m.cancelFunc`). Because Bubble Tea copies Model on each Update cycle, goroutine closures capture a specific Model copy, not the latest state.

**Actual risk**: In practice, the goroutines only read `m.program` (pointer, set once) and don't mutate Model fields from goroutines -- mutations go through `program.Send()` back to the Update loop. This is correct. The `safego.Go` closures don't directly write to `m.loading` etc. -- they only call `m.program.Send()`.

**Verdict**: False alarm on close inspection, but the pattern is fragile. A future developer might mistakenly write to Model fields from a goroutine.

**Fix**: Add a comment near the Model struct documenting the invariant: "Model fields must only be mutated inside Update(). Goroutines must use program.Send() to communicate state changes."

#### M-7: No validation on ApprovalMsg/DiffConfirmMsg response channels

**Files**: `internal/tui/model_update.go` (approval handling), `internal/tui/model.go` (ApprovalMsg struct)

**Problem**: `ApprovalMsg` and `DiffConfirmMsg` contain `Response chan permission.Decision` and `Response chan bool` respectively. If the TUI handler sends to these channels but the agent goroutine has already timed out or been cancelled, the channel send will block forever (if unbuffered) or panic (if closed). There is no select-on-context-done guard when sending to these channels.

**Impact**: Potential goroutine leak if agent goroutine cancels while TUI is waiting for user approval. In practice, `cancelActiveRun()` handles this by resetting the agent state, but the channel send in the approval handler could still block.

**Fix**: Use buffered channels (capacity 1) for approval/diff responses (they may already be -- verify). Add a comment documenting the channel lifecycle.

### LOW

#### L-1: Spinner handled separately from main Update switch

**File**: `internal/tui/model_update.go`, lines 22-26

```go
var spinnerCmd tea.Cmd
if m.spinner.IsActive() {
    spinnerCmd = m.spinner.Update(msg)
}
```

**Problem**: The spinner is updated BEFORE the type switch. This means every message type (including ones the spinner doesn't care about) triggers a spinner update, which returns a non-nil `spinnerCmd` for blink scheduling. This is then combined with the actual handler's cmd via `combineCmds`. While functionally correct, it adds overhead for every message.

**Impact**: Negligible performance cost. Correctness is preserved because `combineCmds` handles nil cmds.

**Fix**: Only call `m.spinner.Update(msg)` for `spinnerMsg` type messages, or move it into a dedicated case.

#### L-2: parseApprovalReply prefix matching too aggressive

**File**: `internal/tui/model_update.go`, lines 662-667

```go
if strings.HasPrefix(t, "y") && len(t) <= 3 {
    return permission.Allow, true
}
if strings.HasPrefix(t, "n") && len(t) <= 3 {
    return permission.Deny, true
}
```

**Problem**: Any IM message starting with "y" or "n" up to 3 characters is interpreted as an approval response. This means "yo", "nah", "noo" (in lower case) would all be treated as approval decisions. In multilingual contexts, short words starting with y/n in other languages could be misinterpreted.

**Impact**: Low -- IM approval responses are typically explicit, and the longer-form Chinese options ("好", "好的", "允许") are handled separately. But a user saying "yep that's wrong" via IM could accidentally approve.

**Fix**: Tighten the prefix matching to exact single-character matches or add a word-boundary check.

#### L-3: autocomplete state rebuilt on every catchall keypress

**File**: `internal/tui/model_update.go`, lines 638

```go
m.updateAutoComplete()
```

**Problem**: In the catchall branch of Update (for unmatched KeyPressMsg), `updateAutoComplete()` is called on every keystroke. If the autocomplete function scans the commands list, this could be wasteful when the input doesn't start with `/`.

**Impact**: Minor -- the commands list is small and the autocomplete check is likely lightweight.

**Fix**: Add a `strings.HasPrefix(m.input.Value(), "/")` guard before calling `updateAutoComplete()`.

#### L-4: i18n sub-catalogs for IM adapters not compared for completeness

**Files**: `internal/tui/i18n_command.go`, `i18n_dingtalk.go`, `i18n_discord.go`, `i18n_feishu.go`, `i18n_harness.go`, `i18n_home.go`, `i18n_im.go`, `i18n_irc.go`, `i18n_matrix.go`, `i18n_mattermost.go`, `i18n_nostr.go`, `i18n_pc.go`, `i18n_provider.go`, `i18n_qq.go`, `i18n_qr_overlay.go`, `i18n_signal.go`, `i18n_slack.go`, `i18n_tg.go`, `i18n_twitch.go`, `i18n_wechat.go`, `i18n_wecom.go`

**Problem**: The i18n system uses sub-catalogs organized by feature area (21 sub-catalog files). These register their keys via `registerSubCatalog()` calls at init time. The main EN/ZH catalogs don't cover these sub-catalog keys. There is no automated check that sub-catalogs have matching EN/ZH entries.

**Impact**: Sub-catalog key drift could cause untranslated strings. The sub-catalog system itself is well-designed (fallback to EN, then to key), so user-facing impact is graceful degradation.

**Fix**: Consider adding a test that enumerates all sub-catalog keys and verifies ZH coverage.

#### L-5: combineCmds nil handling could be simplified

**File**: `internal/tui/model_update.go`, line 580

```go
return m, combineCmds(spinnerCmd, cmd)
```

**Problem**: `combineCmds` is used throughout to merge the spinner tick cmd with handler-specific cmds. The implementation is not shown but presumably handles nil cmds. This pattern is repeated 15+ times. A cleaner approach would be to always batch cmds at the end of Update rather than per-handler.

**Impact**: Style/preference only.

#### L-6: Slash command dispatch does not validate argument count

**File**: `internal/tui/commands_slash.go`

**Problem**: The slash command dispatcher parses arguments but does not validate argument counts against the command's `Arguments` spec from the frontmatter. A command expecting `<arg1> <arg2>` will receive whatever the user types, including 0 or 3+ arguments.

**Impact**: Commands handle their own argument validation internally, so this is not a correctness issue. But better UX would show usage hints for mismatched argument counts.

#### L-7: Session resume does not restore approval/questionnaire state

**File**: `internal/tui/model_update.go`, lines 102-118

**Problem**: When resuming a session via `sessionResumeLoadedMsg`, the conversation history is restored but any in-progress approval or ask_user questionnaire state is lost. If a session was interrupted during an approval prompt, the resumed session will show the approval message in history but won't have an active approval handler.

**Impact**: Expected behavior -- the approval channel is gone. But worth documenting that approval state is ephemeral and not persisted.

---

## State Machine Analysis

### Agent Lifecycle States

The TUI uses implicit state via boolean flags rather than an explicit state machine:

| State | `loading` | `cancelFunc != nil` | `runCanceled` | Active Panel |
|-------|-----------|---------------------|---------------|--------------|
| Idle | false | nil | false | none or any |
| Agent Running | true | non-nil | false | none |
| Approval Pending | true | non-nil | false | ApprovalMsg shown |
| AskUser Pending | true | non-nil | false | AskUserMsg shown |
| Cancelled | false | nil | true (briefly) | none |
| Done | false | nil | false (reset) | none |

### Transitions

1. **Idle -> Agent Running**: `submitText()` -> `startAgent()` sets `loading=true`, `cancelFunc=cancel`
2. **Agent Running -> Approval Pending**: `ApprovalMsg` received, agent pauses waiting for response channel
3. **Agent Running -> AskUser Pending**: `AskUserMsg` received, questionnaire UI shown
4. **Approval Pending -> Agent Running**: User selects y/n, response sent to channel
5. **Any Active -> Cancelled**: ESC/Ctrl+C -> `cancelActiveRun()` sets `runCanceled=true`, calls cancel()
6. **Agent Running -> Done**: `agentDoneMsg` resets all flags
7. **Done -> Idle**: Automatic (flags already reset)
8. **Done -> Agent Running**: If pending submissions exist, `submitPendingSubmissionCmd()` auto-starts next

### Correctness Assessment

- **RunID tracking** (`activeAgentRunID`) prevents stale messages from completed runs from affecting current state. Checked in `agentDoneMsg`, `agentErrMsg`, `agentInterruptMsg`, `agentStatusMsg`, `agentRoundSummaryMsg`. **Correct**.
- **Double-cancel protection**: `cancelActiveRun()` checks `m.runCanceled` before proceeding. **Correct**.
- **Pending queue atomicity**: Uses `*pendingQueue` with mutex. `consumeDetailed()` coalesces consecutive non-hidden submissions. **Correct**.
- **Shutdown cascading**: `cancelActiveRun()` calls `subAgentMgr.CancelAll()` and `swarmMgr.CancelAll()`. `shutdownAll()` also calls both. **Correct**.
- **Exit confirm**: First ESC triggers `promptExitConfirm()`, second ESC exits. Panel close takes priority over exit confirm via `closeActivePanel()`. **Correct**.

### Potential Race Conditions

1. **Webchat message during approval**: If a webchat message arrives while `ApprovalMsg` is displayed, `webchatUserMsg` handler checks `m.cancelFunc == nil` for idle state. If agent is running (waiting for approval), it queues via `m.queuePendingSubmission()`. The pending submission will be processed after the approval completes and the current run finishes. **Correct**.
2. **Concurrent program.Send from multiple goroutines**: bubbletea's `program.Send()` is documented as goroutine-safe. All call sites are within goroutines. **Correct**.

---

## Keyboard Shortcut Analysis

| Key | Context | Action | Conflict? |
|-----|---------|--------|-----------|
| Enter | Idle | Submit input | No |
| Enter | Panel active | Panel action | No (routed to panel) |
| ESC | No panel | Exit confirm / cancel | No |
| ESC | Panel open | Close panel | No |
| Ctrl+C | Any | Cancel run / exit | No (first cancels, second exits) |
| Ctrl+D | Any | Quit | No |
| Tab | Idle, `/` prefix | Autocomplete cycle | No |
| Shift+Tab | Idle, `/` prefix | Autocomplete reverse | No |
| Ctrl+P | Idle | Open provider panel | Potential conflict with readline ctrl-p |
| Ctrl+M | Idle | Open model panel | Potential conflict with carriage return |
| Ctrl+L | Any | Toggle sidebar | No |
| Ctrl+V | Input | Paste | No |
| Ctrl+I | Input | Open file browser | No |
| Ctrl+B | Input | Attach image/clipboard | No |
| Ctrl+N | Idle | New session | No |
| Ctrl+S | Idle | Open stats panel | No |
| Up/Down | Autocomplete | Navigate suggestions | No |
| Alt+Up/Down | Any | Scroll conversation | No |

### Potential Conflicts

- **Ctrl+P** in terminal contexts is typically "previous command" in readline. Since the TUI uses its own input (not readline), this is safe.
- **Ctrl+M** maps to carriage return (0x0D) in terminals. If the terminal emulator sends both Ctrl+M and Enter as the same key, this could cause unexpected model panel opening. Need to verify bubbletea v2 distinguishes these.

---

## Commands Package Analysis

### Registration and Dispatch

- **Bundled skills** (`bundled.go`): 6 built-in skills (verify, plan, implement, review, debug, investigate). Each has name, description, when_to_use, template. **Well-structured**.
- **File-based loading** (`loader.go`): Scans `~/.ggcode/skills/`, `~/.ggcode/commands/`, `<project>/.ggcode/skills/`, `<project>/.ggcode/commands/`. Skills use `<name>/SKILL.md` format, commands use `<name>.md` format. **Correct**.
- **Manager** (`manager.go`): Thread-safe with `sync.RWMutex`. Signature-based change detection for reload. Merges bundled + extra providers + file-loaded commands. **Correct**.
- **Override order**: File-loaded commands override bundled skills (last-write-wins in `combinedCommands`). Project-local overrides global. **Correct**.

### Findings

No significant issues in the commands package. The loader correctly handles missing directories, YAML parse errors, and deduplicates load targets via symlink resolution.

---

## Recommendations

1. **Immediate**: Add 13 missing zh-CN i18n keys (H-1).
2. **Short-term**: Extract shared `resetAgentRunState()` method to deduplicate done handlers (H-2).
3. **Short-term**: Add IM final text emit to `handleAgentDoneMsg` (M-1).
4. **Medium-term**: Add panel viewport resize propagation in `handleResize()` (M-4).
5. **Medium-term**: Add i18n completeness test (L-4).
6. **Documentation**: Add invariant comment on Model struct about goroutine safety (M-6).
