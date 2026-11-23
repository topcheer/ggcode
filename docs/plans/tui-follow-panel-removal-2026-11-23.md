# Plan: Remove the TUI sub-agent / teammate "follow" panel — 2026-11-23

Status: **PROPOSAL (analysis only, no code changed).** First step of the TUI
rendering diet requested by the user: delete the follow panel logic for
subagents, teammates and named agents outright.

## 1. What the feature is (verified in code)

`Ctrl+N` toggles a "follow" mode: while sub-agents (`spawn_agent`) or swarm
teammates are running, a **strip** of slots is drawn above the input
(`renderSubAgentFollowStrip`), `Ctrl+N` enters the first slot, `↑/↓/←/→`
cycles slots, and the **main conversation area is replaced** by a live
replay of that child's context (text / reasoning / tool calls / results,
rebuilt from `subagent.Manager` / `swarm.Manager` snapshots). `Ctrl+C` or
`Esc` leaves follow mode. Named agents (`use_namedagent`) are plain
sub-agents, so they appear in the same slots — there is **no separate
named-agent panel code** in the TUI (only tool-label/registration code in
`repl.go` / `tool_labels*.go`, untouched by this plan).

## 2. Inventory (lines are current)

### 2.1 Follow-only — delete outright

| File | Lines | What |
|---|---|---|
| `internal/tui/subagent_follow.go` | 636 | `subAgentFollowState` (slots/views/dirty/rebuild), `followSlot`, `followViewEntry`, `followEventData`, `buildFollowList`, snapshot→data converters, `renderSubAgentFollowStrip`, `shouldRebuild`, `cleanup`, `hasTerminalSlots`, `subAgentGracePeriod`, `stripRefreshInterval`, `textThrottleMap`, `shortID` (TUI copy; `extpane` has its own) |
| `internal/tui/update_subagent.go` | ~120 of 392 | `scheduleFollowGraceTick`, `handleSubAgentFollowRefreshMsg`, `handleFollowGraceTickMsg`, the follow bookkeeping block inside `handleSubAgentUpdateMsg` (everything after the tunnel push), the grace-tick scheduling at the end of `handleSubAgentDoneMsg` |
| `internal/tui/model.go` | 1 | field `subAgentFollow subAgentFollowState` |
| `internal/tui/model_messages.go` | 2 | `subAgentFollowRefreshMsg`, `followGraceTickMsg` |
| `internal/tui/model_update_dispatch.go` | ~3 | handler registration for the two msgs |
| `internal/tui/update_keys.go` | ~55 | `ctrl+n` toggle (L472–482), `ctrl+c` while following (L129–131), `esc` while following (L648–650), and the four `↑ ↓ ← →` slot-cycling blocks (L544–601); plus the **dead empty `case "ctrl+p"`** (L483) |
| `internal/tui/view_status.go` | ~30 | `hint.follow_panel` / `hint.unfollow_panel` hint, `followStrip` above the input, follow-active replacement of the input line (L231–262) |
| `internal/tui/view_chat.go` | ~20 | the `if m.subAgentFollow.isActive()` branch that swaps the conversation for the child's `chat.List` |
| `internal/tui/i18n_*.go` × 10 locales | ~130 | 9 keys each: `hint.follow_panel`, `hint.unfollow_panel`, `follow.loading`, `follow.active_agent`, `follow.active_teammate`, `follow.status_running`, `follow.status_done`, `follow.more`, `follow.hint`; plus the `/help` line "Ctrl+N Toggle sub-agent follow panel" (en) / "Ctrl+N folgen" (de, …) |
| `internal/tui/subagent_follow_test.go` | 454 | 22 tests, all follow-state/throttle |
| `internal/tui/subagent_follow_1761_test.go` | 51 | 2 tests (tool-result FIFO into follow list, strip anchor scroll) |
| `internal/tui/pty_follow_test.go` → `TestPTY_SubAgentFollowDeepWorkout` | ~200 of 549 | PTY end-to-end follow workout (the `TestAdd`/`TestMultiply`… funcs in that file are fixtures used by PTY tests — **keep the file, drop only this test**) |
| `internal/tui/subagent_done_test.go` → `TestSubAgentDoneMsg_BusyAgentSchedulesGraceCleanup` | ~28 | asserts the grace tick that only exists for slot expiry |

**Estimated removal: ~1,700 lines** (≈ 1,100 source + ≈ 600 tests/i18n).

### 2.2 Shared — must NOT be touched (easy to confuse by name)

| Symbol | Why it stays |
|---|---|
| `pushSubAgentTunnelEvent`, `pushSubAgentTunnelStreamText/Reasoning/ToolCall/ToolResult`, `pushSwarmTunnelEvent` (`tunnel.go`) | These feed the **WebUI relay** (`webui`/tunnel stream), not the follow panel. The tunnel msg handlers in `update_subagent.go` contain no follow code — keep them whole |
| `extPaneWrite*`, `extPaneHandleSwarmEvent`, `extPaneHandleDone`, `extpane/` package (1,520 L) | **External panes** (tmux / iTerm2 / kitty splits) — a separate feature that mirrors child output outside the TUI |
| `handleSubAgentDoneMsg` chat notice + `formatIdleReportDigest` | Done notices in the parent chat and the **idle-wake digest (#5700)** — core multi-agent behaviour |
| `handleSubAgentSystemMsg` | Sub-agent status lines ("[Retry 1/…]") in the main chat |
| `view_sidebar_swarm.go`, `refreshSwarmTeamsSnapshot`, `swarmEventAffectsSidebar` | **Sidebar "Swarm Teams" section (#5851)** — different surface, different feature |
| `chatListFollowOutput()` | "Follow the output" = scroll-to-bottom; nothing to do with agents |
| `repl.go` named-agent tool registration, `tool_labels*.go` | `use_/create_/delete_/list_namedagent` are tools with labels; no panel |

### 2.3 The one real dependency to re-wire

`extPaneResolveName(agentID)` (used by the external-pane feature) looks the
name/kind up **in the follow slot list**. After deletion, resolve from the
source of truth instead:

```go
func (m Model) extPaneResolveName(agentID string) (name, kind string) {
    if m.subAgentMgr != nil {
        if s, ok := m.subAgentMgr.Snapshot(agentID); ok { return s.Name, "subagent" }
    }
    if m.swarmMgr != nil {
        for _, ts := range m.swarmMgr.ListTeamStatuses() {
            for _, tm := range ts.Teammates { if tm.ID == agentID { return tm.Name, "teammate" } }
        }
    }
    return agentID, "subagent"
}
```
Cost: O(teammates) per event, same as before (the old code scanned the slot
slice). Note that terminal sub-agents drop out of `Snapshot` after the
manager prunes them; `extPaneHandleDone` already receives the name from
`subAgentDoneMsg` where it matters.

## 3. Behaviour after removal (what the user will see)

- No strip above the input while children run; no `Ctrl+N` entry; `↑/↓/←/→`
  keep their normal meaning at all times (today they are hijacked while
  following).
- `Ctrl+C`/`Esc` never get swallowed by follow mode → cancel/exit prompts
  behave as without children.
- Child progress still reaches the user through the channels that remain:
  main-chat status lines + done notices (`✓ sub-agent X finished…`),
  idle-wake digest, sidebar Swarm Teams section, `/inspector` /
  `list_agents` / `wait_agent`, external tmux panes, WebUI tunnel stream.
- `Ctrl+N` becomes a no-op key exactly as it already is when no slots
  exist (the case is removed entirely; `ctrl+p` was already an empty case).

## 4. Execution order (one PR, ~8 steps, each verifiable)

1. **Kill the UI surfaces first** (so the state becomes unreachable):
   `view_chat.go` branch → `view_status.go` (hint + strip + input swap) →
   `update_keys.go` (4 arrow blocks, ctrl+n, ctrl+c, esc, dead ctrl+p).
   *Check:* `go build ./internal/tui` — unused-method errors show exactly
   what is now dead.
2. **Delete the state file** `subagent_follow.go` + `model.go` field +
   `model_messages.go` types + dispatch registrations.
3. **Prune `update_subagent.go`**: remove the two follow handlers, the
   bookkeeping block in `handleSubAgentUpdateMsg` (keep the tunnel push at
   the top), the grace-tick line in `handleSubAgentDoneMsg`,
   `scheduleFollowGraceTick`.
4. **Re-wire `extPaneResolveName`** per §2.3.
5. **i18n**: remove the 9 keys in all 10 locales + the `/help` line. Also
   remove the stale `Ctrl+N/P  新建/上一会话` help rows in de/es/fr/ja/ko
   (they document a session-nav binding that does not exist in the TUI —
   `ctrl+p` was an empty case). *Decision needed: see §6.*
6. **Tests**: delete `subagent_follow_test.go`, `subagent_follow_1761_test.go`,
   `TestPTY_SubAgentFollowDeepWorkout`, adjust `subagent_done_test.go`
   (drop the grace-cleanup case; keep the 7 behaviour tests). Keep
   `view_sidebar_swarm_test.go`, `repl_subagent_test.go`.
7. **Docs**: `docs/guide/multi-agent-modes.md:212` ("Follow panel … same as
   subagents") → replace with the surviving observation channels; check
   README/README_zh for "follow" wording.
8. **Verify**: `gofmt -l`, `go vet ./internal/tui/`, `go test -tags goolm
   ./internal/tui/` (incl. PTY suite), `go build -tags goolm ./...`;
   `grep -rn "subAgentFollow\|followSlot\|followGraceTick\|FollowStrip"
   internal/tui` must return 0 hits.

## 5. Verification matrix (end-to-end, manual PTY or live TUI)

| Scenario | Expected |
|---|---|
| Spawn 2 sub-agents | no strip, no `Ctrl+N` hint; status lines + done notices still appear in chat; `/inspector` lists them |
| Spawn a teammate (`swarm`) | sidebar Swarm Teams section lists the team; done/idle events still arrive; no strip |
| `use_namedagent` run | same as sub-agent |
| Press `Ctrl+N`, `Ctrl+P`, arrows while children run | nothing hijacked; arrows scroll history as usual |
| `Ctrl+C` while children run | normal cancel/exit prompt |
| `tmux` + external panes enabled | child panes still created with correct **names** (validates §2.3) |
| `/webui` open | child stream still flows to the browser (tunnel untouched) |
| Idle parent, child finishes | parent still wakes with the digest (#5700) |
| `prefers-reduced-motion` n/a; rendering | main chat area never replaced |

## 6. Open decisions for the user

| # | Question | Recommendation |
|---|---|---|
| D1 | Scope: also remove the **sidebar Swarm Teams section** (#5851, ~220 L incl. tests)? | **No** in this PR — it is a cheap static list, not a live-render cost; separate decision |
| D2 | Also remove the **external-pane** (`extpane`, 1,520 L) mirroring? | **No** — different feature (tmux splits); it only shares the event handlers |
| D3 | Free `Ctrl+N` for something else (e.g. new session, as the stale de/es/fr/ja/ko help claims)? | Leave unbound in this PR; delete the stale help rows to stop the false promise |
| D4 | Should `subAgentDoneMsg` keep the **1-minute grace** concept? | The grace exists only to keep finished slots visible in the strip; with no strip there is nothing to keep → remove with the panel (done notices are unaffected) |

## 7. Risks

- **Hidden reader of follow state**: the symbol grep in §4.8 is the guard; `go build` fails on any missed reference since the type is unexported.
- **PTTY test fixture coupling**: `pty_follow_test.go` hosts unrelated fixtures; delete only the follow test.
- **i18n drift**: removing keys in 10 locales by hand is error-prone → do it with one scripted pass and verify the per-locale key counts match again.
- **Key-binding surprise**: users who used `Ctrl+N` to peek lose the feature by design; the help text must change in the same PR.
