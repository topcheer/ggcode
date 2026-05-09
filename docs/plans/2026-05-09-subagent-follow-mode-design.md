# Subagent Follow Mode for TUI

## Problem

The TUI already shows the main agent conversation in the primary content panel, but sub-agents are still mostly observable through indirect surfaces: `spawn_agent` / `wait_agent` tool output, `/agents`, `/agent <id>`, and high-level activity/status updates. The requested UX is stronger:

- when sub-agents are running, show a compact follow strip near the composer
- let the user jump into a specific running sub-agent's live output
- while following a sub-agent, replace the main conversation panel with that sub-agent's output
- while following, disable normal input until the user exits follow mode
- if the followed sub-agent finishes, automatically return to the main agent panel

Scope decision for this design: **only running sub-agents are followable**. Completed sub-agents do not remain switchable in follow mode; they continue to be inspectable through existing `/agent <id>` and chat/tool history surfaces.

## Review of the prior analysis

The prior analysis is directionally right, but it overstates how much new plumbing is needed.

### What it got right

1. We do need a **panel-switching state** in the TUI model.
2. We do need **new rendering logic** for a sub-agent follow pane and a follow strip.
3. We do need **key handling changes** so the user can enter/exit follow mode.
4. We do need to think about **multiple concurrent sub-agents** and deterministic ordering.

### What it missed or overstated

1. **Sub-agent event capture already exists.**  
   `internal/subagent/runner.go` already records text/tool/result/error events into `SubAgent.events`, and `internal/subagent/manager.go` already exposes snapshots with those events.

2. **The TUI already receives live sub-agent update notifications.**  
   `internal/tui/repl.go` wires `SetOnUpdate` / `SetOnComplete` to `program.Send(subAgentUpdateMsg{})`. The missing part is not "push sub-agent events into TUI for the first time", but "materialize those existing snapshots into a follow view".

3. **We do not need a new cross-layer event bus for V1.**  
   `maxAgentEvents` is capped at 200, so snapshot-derived rendering is bounded. With lazy view creation and throttled rebuilds, this is cheap enough for a first implementation.

4. **`Shift+1/2/3` needs terminal-specific care.**  
   In terminals, `Shift+1` is often delivered as the literal `!`, not as a rich modifier event. The UX can still present numbered shortcuts, but the implementation must decode terminal-safe key forms rather than assuming GUI-style key semantics.

## Review feedback assessment

The later review is reasonable and should be incorporated. Two points are especially important for a smooth V1:

1. **High-frequency update coalescing.**  
   `subagent.Run` can call `Manager.Notify()` on every text chunk and tool event. Even with a 200-event cap, rebuilding a derived `chat.List` on every update can compete with Bubble Tea rendering. V1 should coalesce follow-list rebuilds with a small minimum interval.

2. **Explicit truncation signaling.**  
   The manager currently drops old events once `maxAgentEvents` is reached, but the snapshot does not expose that anything was dropped. A follow pane should not silently make the sub-agent look like it only produced the visible tail.

The tool-card concern is also valid: sub-agent events currently store compact `ToolName`, `ToolArgs`, and `Result` strings, not the full structured data that the main agent tool cards sometimes use for rich renderers. V1 should therefore promise **compact, visually consistent tool cards**, not full fidelity with every main-agent tool renderer. Full-fidelity sub-agent cards can be a later event-schema enhancement.

## Goals

- Show active sub-agents as first-class live work in the TUI.
- Reuse the existing `chat.List` rendering style so followed sub-agent output feels like the main agent view.
- Keep follow mode bounded to active work only.
- Avoid invasive changes to sub-agent execution architecture.
- Preserve current `/agent` and `/agents` commands as the detailed/diagnostic fallback.
- Keep follow-pane rebuilds cheap under high-frequency text/tool events.

## Non-goals

- Persisting a separate follow-history UI for completed sub-agents.
- Building a generic multi-pane window manager in the TUI.
- Replacing `/agent <id>` with the new follow view.
- Streaming raw provider events directly into the TUI from background goroutines.

## Design options

### Option A — Snapshot-derived follow pane (recommended)

Use the existing `subagent.Manager` as the source of truth. On each `subAgentUpdateMsg`, the model refreshes a derived follow state:

- list of currently running sub-agents
- numbered shortcut slots
- a lazily built `chat.List` for selected/followed sub-agents, derived from the latest snapshot

When the user activates a slot, `renderConversationPanel()` shows that derived list instead of the main `chatList`.

**Pros**

- smallest architecture change
- leverages existing `Snapshot` + capped event buffer
- easy to reason about thread-safety
- keeps main/sub-agent rendering aligned by reusing `chat.List`

**Cons**

- rebuilds derived UI state instead of receiving richer incremental messages; this is mitigated with lazy build + throttling
- sub-agent rendering fidelity is limited to what is already stored in `Snapshot.Events`

### Option B — Dedicated follow context panel

Show sub-agent output in a modal/context panel below the main conversation instead of replacing the main pane.

**Pros**

- less disruption to the main conversation layout
- easier input handling

**Cons**

- does not match the requested "cover the main content panel and follow scroll like the main agent"
- wastes vertical space in a terminal

### Option C — Full realtime sub-agent event bus

Push structured sub-agent stream events into Bubble Tea as first-class messages, similar to the main agent stream path.

**Pros**

- highest long-term flexibility
- supports richer replay, per-event animation, and future nested interactions

**Cons**

- significantly larger change set
- duplicates logic already stored in `Snapshot.Events`
- not needed for the requested V1 behavior

## Recommendation

Implement **Option A**.

The current architecture already gives us the hard parts: sub-agent lifecycle, bounded event history, snapshotting, and TUI wakeups. The missing piece is a derived follow-mode presentation layer, not a new execution model. This keeps the feature incremental and behavior-safe while still delivering the requested UX.

## Proposed architecture

### 1. Model state

Add a dedicated follow state to `internal/tui/model.go`.

```go
type subAgentFollowState struct {
    activeID         string
    slots            []subAgentFollowSlot
    views            map[string]*chat.List
    dirty            map[string]bool
    lastBuild        map[string]time.Time
    refreshScheduled bool
}

type subAgentFollowSlot struct {
    AgentID      string
    Index        int
    Label        string
    StatusText   string
    ShortcutHint string
}
```

`activeID == ""` means the main agent conversation is shown.  
`activeID != ""` means the conversation panel should render the followed sub-agent view.

This state is derived from `subAgentMgr` and should be treated as a UI cache, not a second source of truth. `dirty`, `lastBuild`, and `refreshScheduled` are only for coalescing expensive list rebuilds; sub-agent lifecycle state remains owned by `subagent.Manager`.

### 2. Message wiring

Change `subAgentUpdateMsg` from an empty struct to a targeted update:

```go
type subAgentUpdateMsg struct {
    AgentID string
}

type subAgentFollowRefreshMsg struct{}
```

In `internal/tui/repl.go`, keep the existing callback structure but send the sub-agent ID:

```go
r.program.Send(subAgentUpdateMsg{AgentID: sa.ID})
```

The update message should carry only the ID, not the `*SubAgent` pointer, so the Bubble Tea loop always re-reads state through `subAgentMgr.Snapshot()` / `List()` and avoids pointer aliasing across goroutines. `subAgentFollowRefreshMsg` is emitted by a short timer when a dirty active follow view needs rebuilding after the throttle window.

### 3. Derived snapshot sync

Add a helper in a new file such as `internal/tui/subagent_follow.go`:

```go
func (m *Model) syncSubAgentFollowState(updatedID string)
```

This helper should be cheap and metadata-oriented. It should:

1. collect running sub-agents from `m.subAgentMgr.List()`
2. keep a stable ordering (recommended: `StartedAt`, then `CreatedAt`, then `ID`)
3. rebuild the slot list for at most 9 visible entries
4. mark the updated agent dirty
5. remove ended agents from the follow cache
6. if the active followed agent is no longer running, clear `activeID` and return to the main panel
7. rebuild the active view immediately only if it is dirty and outside the throttle window

Inactive views should be built lazily when selected. There is no reason to rebuild every running sub-agent's `chat.List` for every update; the strip only needs metadata.

### 4. Follow rebuild throttling

Use a small throttle window, e.g. `100ms`, for rebuilding the active followed agent's derived `chat.List`.

Recommended behavior:

1. Every `subAgentUpdateMsg{AgentID}` refreshes slots and marks that ID dirty.
2. If the dirty ID is not active, do not rebuild its view yet.
3. If the dirty ID is active and `time.Since(lastBuild[id]) >= 100ms`, rebuild immediately and scroll to bottom.
4. If the dirty ID is active but inside the throttle window, schedule one `subAgentFollowRefreshMsg` for the remaining delay.
5. When `subAgentFollowRefreshMsg` arrives, rebuild the active dirty view if the active sub-agent is still running.

This keeps UI responsiveness stable during rapid text streaming without adding a new event bus.

### 5. Event truncation metadata

Update `internal/subagent/manager.go` so dropped events are visible in snapshots:

```go
type SubAgent struct {
    ...
    eventsDropped int
}

type Snapshot struct {
    ...
    EventsDropped int
}
```

`appendEvent` should increment `eventsDropped` whenever it discards the oldest event. The follow pane can then render an explicit top notice:

```text
Early output truncated — showing the latest 200 events.
```

Do not infer truncation from `len(Events) == maxAgentEvents`; an agent can legitimately have exactly 200 events with nothing dropped if the cap changes or if event retention is later made configurable.

## Rendering design

### 1. Main conversation panel switching

Update `renderConversationPanel()` in `internal/tui/view.go`:

- if `follow.activeID == ""`, render the existing `m.chatList`
- otherwise render the followed sub-agent's derived `chat.List`

This is the key architectural point: **the followed sub-agent uses the same main content area**, not a side panel or context panel.

### 2. Follow strip

Add a new render helper, for example:

```go
func (m Model) renderSubAgentFollowStrip() string
```

Placement: directly above the composer input inside `renderComposerPanel()`.

Example:

```text
🤖  1 parser-investigation · Reading docs/spec.md   2 release-build · running tests   Esc close
```

When one sub-agent is actively followed:

```text
🤖  [1 parser-investigation · following]   2 release-build · running tests   Esc close
```

The strip should:

- only appear when there is at least one running sub-agent
- show short human-readable labels derived from `DisplayTask`
- show each sub-agent's latest summary from `ProgressSummary` / `CurrentTool` / `CurrentPhase`
- highlight the active slot
- show overflow as `+N more` if the running set exceeds visible shortcut slots

### 3. Composer behavior while following

When `follow.activeID != ""`:

- the textarea should not accept normal input
- the composer box stays visible, but the input row becomes a disabled notice

Example:

```text
Following sub-agent sa-2 — input paused. Press Esc or the same shortcut to return.
```

This preserves the layout and keeps the "TUI is still alive" feeling, while making it obvious why typing does nothing.

## Rendering sub-agent output

### Event-to-chat-item translation

Each followed sub-agent view should be derived from `subagent.Snapshot.Events`.

Recommended mapping:

- `AgentEventText` -> assistant text item
- `AgentEventToolCall` -> compact running tool item
- `AgentEventToolResult` -> compact success/error tool completion item
- `AgentEventError` -> system/error item

Add a helper such as:

```go
func buildSubAgentFollowList(snap subagent.Snapshot, styles chat.Styles, width, height int) *chat.List
```

This helper should reuse the existing `chat` package renderers instead of inventing a second rendering style. The sub-agent should feel like "the same UI, different conversation source".

For V1, tool cards are **compact tool cards**, not guaranteed full-fidelity copies of main-agent cards. Current `AgentEvent` data has compact strings:

- `ToolName`
- `ToolArgs` truncated to 300 characters
- `Result` truncated to 500 characters

That is enough for clear live-follow visibility, but not enough for every specialized renderer that depends on richer raw data such as full edit diffs, full command output, or line-count metadata. The builder should pair tool call/result events by event order and tool name, then render a generic `chat` tool item with localized display name, compact arguments, result preview, and error state.

If full-fidelity cards become necessary later, extend `AgentEvent` with structured fields such as `ToolCallID`, raw argument JSON, result metadata, and explicit truncation flags. Do not block the V1 follow mode on that schema expansion.

### Header treatment

The follow list should begin with a small synthetic system item describing the sub-agent:

- task label
- sub-agent ID
- current status

That keeps the user oriented after switching into a different pane.

If `Snapshot.EventsDropped > 0`, render the truncation notice immediately after the header and before current events.

### Scroll behavior

Whenever the active followed sub-agent receives an update, its derived `chat.List` should scroll to bottom, matching the main agent's normal live-follow behavior.

## Key handling and terminal compatibility

### Why raw `Shift+1` is risky

In many terminals:

- `Shift+1` becomes `!`
- `Shift+2` becomes `@`
- `Shift+3` becomes `#`

So the implementation must not depend on GUI-style modifier reporting.

### V1 shortcut plan

1. The UI presents **numbered follow slots**.
2. The handler accepts terminal-safe key forms for those slots (`!`, `@`, `#`, etc.).
3. `Esc` exits follow mode.
4. Pressing the currently active slot again toggles follow mode off.

To avoid stealing normal typing too aggressively, slot entry should only trigger when:

- there is at least one running sub-agent
- no modal/context panel is open
- autocomplete is not active
- the composer is empty

Once follow mode is active, input is already disabled, so toggle handling is unambiguous.

## Lifecycle behavior

### Enter follow mode

1. user presses a slot shortcut
2. model resolves slot -> sub-agent ID
3. `follow.activeID` is set
4. conversation panel switches to the sub-agent list
5. composer enters disabled/following state

### Exit follow mode

1. user presses `Esc` or the same slot shortcut
2. `follow.activeID` is cleared
3. conversation panel switches back to the main `chatList`
4. composer returns to normal input mode

### Automatic return on completion

If the active followed sub-agent transitions out of `pending/running`:

1. remove it from running slots
2. clear `follow.activeID`
3. return to the main panel automatically
4. optionally emit one concise system note in the main chat, e.g. `Sub-agent sa-2 completed; returned to main view.`

This matches the scope decision that completed sub-agents are not kept as follow targets.

## Error handling

- **Missing snapshot during refresh**: drop the slot and, if active, return to main view
- **Late update for an ended sub-agent**: ignore
- **More than 9 running sub-agents**: render first 9 shortcuts and a `+N more` indicator
- **Empty event list**: render a placeholder like `Waiting for first output…`
- **Dropped historical events**: render an explicit truncation notice at the top of the follow pane
- **High-frequency updates**: coalesce active follow-list rebuilds with the throttle window; do not enqueue unbounded refresh timers
- **Manager absent**: hide the strip and behave exactly as today

No silent failures should be introduced; unexpected transitions should at least hit `debug.Log`.

## Files expected to change

### Existing files

- `internal/tui/model.go`
- `internal/tui/model_messages.go`
- `internal/tui/repl.go`
- `internal/tui/model_update.go`
- `internal/tui/view.go`
- `internal/subagent/manager.go`

### New file

- `internal/tui/subagent_follow.go`

### Likely tests

- `internal/tui/layout_test.go`
- `internal/tui/tui_test.go`
- potentially `internal/tui/keyboard_interaction_test.go`

## Test plan

1. **Follow strip rendering**
   - hidden when no running sub-agents
   - shows stable slot order
   - highlights active slot

2. **Pane switching**
   - main panel renders main chat by default
   - selecting a running slot replaces the panel with that sub-agent output
   - toggling off restores main chat

3. **Input disabling**
   - composer renders disabled state while following
   - enter/send path does not submit input while following

4. **Completion behavior**
   - followed sub-agent completion auto-returns to main panel
   - non-followed sub-agent completion does not disrupt the current view

5. **Terminal-safe key handling**
   - `!/@/#...` map to slots as intended
   - slot shortcuts do not fire when autocomplete/modal panels are active
   - slot shortcuts do not steal input when the composer already contains draft text

6. **Concurrent sub-agents**
   - multiple running sub-agents update independently
   - updates rebuild only the affected derived view
   - ended agents disappear from the strip without corrupting active state

7. **Rebuild throttling**
   - burst updates for the active followed sub-agent schedule at most one delayed refresh
   - inactive sub-agent updates refresh strip metadata without rebuilding inactive views

8. **Truncation notice**
   - when `EventsDropped > 0`, the follow pane shows the early-output-truncated notice
   - when exactly `maxAgentEvents` are retained but none were dropped, no false truncation notice appears

9. **Compact tool-card fidelity**
   - tool call/result events render readable compact cards
   - error results render as failed cards
   - missing or unmatched result events leave the tool card in a safe running/unknown state until the next snapshot or completion

## Rollout notes

This feature can be implemented incrementally:

### Phase 1

- add follow strip
- add follow-mode state
- switch conversation panel between main chat and a derived sub-agent chat list
- auto-return on completion
- coalesce active follow-list rebuilds with a small throttle window
- expose `EventsDropped` and show an explicit truncation notice

### Phase 2

- improve shortcut ergonomics if terminal feedback shows issues
- add richer synthetic header/footer items
- optionally allow an inspector handoff from follow mode into `/agent <id>` details
- extend `AgentEvent` for full-fidelity specialized tool-card rendering if needed

## Final assessment

The requested UX is **architecturally feasible with moderate TUI work**, but it does **not** require redesigning sub-agent execution. The current codebase already has the critical primitives:

- bounded event capture in `subagent.Run`
- snapshot-based sub-agent state
- TUI wakeups on sub-agent updates
- reusable `chat.List` rendering infrastructure

So the correct design is: **build a follow-mode presentation layer on top of existing snapshots, not a new streaming subsystem.**
