# Round 9 — TUI

**Scope**: `internal/tui/` (47+ files), `internal/commands/`, `internal/chat/`.

**Date**: 2026-05-29. Round 8 reference: `docs/reviews/round8-tui.md`.

---

## Round 8 findings — verified status

| ID | Title | Status | Evidence | Fix |
|----|-------|--------|----------|-----|
| H-08 | i18n drift (13 zh-CN keys missing) | **PARTIAL** | `internal/tui/i18n_zh.go` — still no completeness check; new localized strings continue to land across many files | Add a unit test asserting key parity between `i18n_en.go` and `i18n_zh.go` |
| H-09 | Done-handler duplication | **OPEN** | `internal/tui/update_done.go:12-43`, `48-82` — `handleDoneMsg()` and `handleAgentDoneMsg()` duplicate almost all reset logic | Extract shared `resetRunState(reason)` helper; both handlers call it; diverge only on tail behavior |
| H-10 | Inconsistent `program.Send` nil checks | **PARTIAL** | `internal/tui/model.go:1006-1013`, `submit.go:101-109,154-160,259,306,333,370,381,436,446,450,452` — multiple goroutine callbacks send without nil guards | Wrap with `m.safeSend(msg)` helper that nil-checks once; use everywhere |
| M-23 | Missing IM emit on `handleAgentDoneMsg` | **OPEN** | `internal/tui/update_done.go:13-38,48-82` — only `handleDoneMsg()` emits final IM stream text | Move IM emit into the shared reset helper |
| M-24 | wechat/wecom dual-close | **OPEN** | `internal/tui/model.go:750-755` — call both specific close and `closeIMPanel()` | Pick one; let `closeIMPanel()` route by panel type |
| M-25 | Panel resize | **RESOLVED** | `internal/tui/resize.go:19-35` — now syncs preview, stats, and file browser viewports | None |
| M-26 | Approval blocking | **PARTIAL** | `internal/tui/model_update.go:657-676` — no context-aware guard visible | Add explicit context cancellation path for approval channel waits |
| M-27 | Spinner overhead | **OPEN** | `internal/tui/model_update.go:31-35` — spinner updates on every message | Gate on `m.loading`; skip when idle |
| M-28 | Aggressive approval prefix match (`y*`/`n*` ≤ 3 chars) | **OPEN** | `internal/tui/model_update.go:670-675` | Remove prefix fallback; require exact tokens; document in IM help |
| M-29 | Autocomplete debounce | **OPEN** | `internal/tui/model_update.go:646-647` — runs on every catchall keypress | Add 50-100ms debounce timer; cancel on next keypress |

---

## New findings (Round 9)

### M — Terminal title not width-truncated

- **Severity**: Medium
- **Files**: `internal/tui/model.go:903-989`
- **Description**: The new dynamic terminal title (v1.3.48) computes `> activity — workspace [model]` with no display-width truncation before OSC emit. Terminal title limits vary by emulator (often 255 bytes / ~80 columns); long CJK titles can be silently truncated by the terminal or, worse, leak into the next line on some emulators.
- **Fix**: truncate `desiredTerminalTitle()` to N display columns (e.g., 120) using a runewidth-aware helper before `terminalTitleWriter(title)`.

### M — `save_memory` live refresh races active runs

- **Severity**: Medium
- **Files**: `internal/tui/model.go:888-895` (`rebuildSystemPrompt`), `internal/tui/submit.go:67-75`
- **Description**: The v1.3.47 `save_memory` live refresh calls `m.systemPromptRebuilder()` and `m.agent.UpdateSystemPrompt()` from the UI path. If a run is in flight, this mutates the agent's system prompt mid-loop, which can confuse the streaming context. The agent loop is not protected by a serializing lock around its system-prompt field.
- **Fix**:
  - Queue the rebuild and have the agent loop apply it at the next safe turn boundary; or
  - Acquire the agent's run mutex (if any) before calling `UpdateSystemPrompt`; or
  - Schedule via `m.program.Send(refreshPromptMsg{})` so it interleaves with run state on the event loop.

### L — Paste hint line not width-clipped

- **Severity**: Low
- **Files**: `internal/tui/paste_placeholder.go:22-52`, `internal/tui/provider_panel.go:331-337`, etc.
- **Description**: The paste hint is appended as a raw line without width-aware clipping. Narrow panels (e.g., 40 columns on a small terminal) overflow.
- **Fix**: in `renderPasteShortcutHint(lang)`, accept an optional width and truncate.

### L — Stats panel rebuilds full string every render

- **Severity**: Low
- **Files**: `internal/tui/stats_panel.go:51-113`
- **Description**: Full summary + full string rebuild on every render. Acceptable for current data sizes but will scale poorly as endpoint-stats grow.
- **Fix**: cache rendered body keyed by `(usageTurnIndex, len(metrics))`; invalidate on update.

### L — `statusActivity` strings vary by code path with hardcoded English

- **Severity**: Low (visibility raised by new features)
- **Files**: `internal/tui/commands_slash_admin.go:448,473`; `internal/tui/commands.go:541`; `internal/tui/tunnel.go:699`; `internal/tui/commands_harness_task.go:33,81`
- **Description**: The new terminal title and the IM emit machinery both surface `statusActivity` text to the user. With these strings hardcoded in English (often inline literals), Chinese users see mixed-language status (terminal title, IM messages).
- **Fix**: introduce `m.setStatusActivity(key string, args ...any)` that always goes through `m.t(...)`; sweep callers.

### L — `m.program.Send(...)` without nil checks (Round 8 H-10 continuation)

- **Files**: `internal/tui/submit.go:101-109, 154-160, 259, 306, 333, 370, 381, 436, 446, 450, 452`
- **Description**: New callers continue to skip nil guard. Late goroutine callbacks (e.g., MCP/IM async completion) can land after program shutdown.
- **Fix**: introduce `m.safeSend(msg tea.Msg)` and replace all direct `m.program.Send(...)` with it.

---

## Notes (not findings)

- `textarea.DynamicHeight` is now bounded (`internal/tui/model.go:372-376`).
- Sidebar default-hidden has discoverability hint in composer (`internal/tui/layout_test.go:1037-1055,1057-1062`).
- MiMo vendor renders via the standard provider panel; no layout overflow found at typical widths.
- Metrics digest in chat is compatible with chat list virtualization (no obvious breakage).

---

## Recommended action items

| Priority | Item |
|----------|------|
| P0 | i18n parity test (H-08) — easy guard against future drift |
| P0 | `save_memory` race serialization (new M) |
| P1 | done-handler dedup (H-09) + IM emit in shared path (M-23) |
| P1 | `safeSend` helper + sweep (H-10) |
| P1 | Terminal title display-width truncate (new M) |
| P2 | Remove approval prefix fallback (M-28) |
| P2 | Autocomplete debounce (M-29) |
| P2 | Spinner gated on `m.loading` (M-27) |
| P3 | `setStatusActivity` i18n helper sweep (new L) |
| P3 | Paste hint width-clip (new L) |
| P3 | Stats panel render cache (new L) |
