package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/util"
)

// ---------------------------------------------------------------------------
// Follow state (unified for sub-agents and swarm teammates)
// ---------------------------------------------------------------------------

// followSlotKind distinguishes sub-agent slots from swarm teammate slots.
type followSlotKind int

const (
	followSlotSubAgent followSlotKind = iota
	followSlotTeammate
)

// followSlot is a unified slot for both sub-agents and swarm teammates.
type followSlot struct {
	ID       string
	Name     string
	Kind     followSlotKind
	Phase    string
	Terminal bool // completed/failed/cancelled/idle
}

type followViewEntry struct {
	list    *chat.List
	dropped int
}

// stripRefreshInterval is the minimum time between follow-strip refreshes.
// This prevents high-frequency teammate_text events from triggering expensive
// refreshSlots/refreshSwarmSlots calls on every streaming token.
const stripRefreshInterval = 500 * time.Millisecond

// subAgentFollowState tracks the TUI follow-mode for sub-agents AND swarm teammates.
// Ctrl+N cycles through all slots regardless of kind.
type subAgentFollowState struct {
	activeID         string                      // agent/teammate ID being followed ("" = main view)
	slots            []followSlot                // ordered list of all followable agents
	views            map[string]*followViewEntry // cached chat lists per agent
	dirty            map[string]bool             // which views need rebuild
	lastRebuild      map[string]time.Time        // throttle: last rebuild time per view
	lastStripRefresh time.Time                   // throttle: last time strip slots were refreshed
	stripDirty       bool                        // true when strip needs refresh on next tick
}

// ---------------------------------------------------------------------------
// Slot management
// ---------------------------------------------------------------------------

// refreshSlots rebuilds the slot list from the sub-agent manager.
// Completed/failed/cancelled sub-agents are excluded — they accumulate
// over time and would clutter the strip. Teammates are handled separately
// in refreshSwarmSlots (they have lifecycle management via team deletion).
// Slots are sorted by ID for stable ordering across refreshes.
// subAgentGracePeriod is how long a completed/failed/cancelled sub-agent
// remains visible in the follow strip before being removed.
const subAgentGracePeriod = 1 * time.Minute

func (f *subAgentFollowState) refreshSlots(saMgr *subagent.Manager) {
	var newSlots []followSlot

	// Sub-agent slots: keep running agents and recently-completed ones (grace period)
	// Uses the lightweight Statuses() API to avoid copying events.
	if saMgr != nil {
		statuses := saMgr.Statuses()
		slices.SortFunc(statuses, func(a, b subagent.StatusInfo) int {
			return cmp.Compare(a.ID, b.ID)
		})
		now := time.Now()
		for _, s := range statuses {
			isTerminal := s.Status == subagent.StatusCompleted || s.Status == subagent.StatusFailed || s.Status == subagent.StatusCancelled
			if isTerminal {
				// Keep in strip during grace period so user can see the result
				if !s.EndedAt.IsZero() && now.Sub(s.EndedAt) < subAgentGracePeriod {
					newSlots = append(newSlots, followSlot{
						ID:       s.ID,
						Name:     s.Name,
						Kind:     followSlotSubAgent,
						Phase:    "done",
						Terminal: true,
					})
				}
				continue
			}
			newSlots = append(newSlots, followSlot{
				ID:       s.ID,
				Name:     s.Name,
				Kind:     followSlotSubAgent,
				Phase:    s.CurrentPhase,
				Terminal: false,
			})
		}
	}

	f.slots = newSlots
}

// refreshSwarmSlots appends swarm teammate slots.
// Uses the lightweight ListTeamStatuses() API to avoid copying teammate events.
func (f *subAgentFollowState) refreshSwarmSlots(swMgr *swarm.Manager) {
	if swMgr == nil {
		return
	}
	// Remove existing teammate slots, keep sub-agent ones
	var subOnly []followSlot
	for _, s := range f.slots {
		if s.Kind == followSlotSubAgent {
			subOnly = append(subOnly, s)
		}
	}
	f.slots = subOnly

	for _, ts := range swMgr.ListTeamStatuses() {
		for _, tm := range ts.Teammates {
			f.slots = append(f.slots, followSlot{
				ID:       tm.ID,
				Name:     tm.Name,
				Kind:     followSlotTeammate,
				Phase:    string(tm.Status),
				Terminal: tm.Status == swarm.TeammateIdle || tm.Status == swarm.TeammateShuttingDown,
			})
		}
	}
	// Sort all slots by ID for stable ordering
	slices.SortFunc(f.slots, func(a, b followSlot) int {
		return cmp.Compare(a.ID, b.ID)
	})
}

// markDirty marks a view as needing rebuild.
func (f *subAgentFollowState) markDirty(agentID string) {
	if f.dirty == nil {
		f.dirty = make(map[string]bool)
	}
	f.dirty[agentID] = true
}

// markStripDirty marks the follow strip as needing a slot refresh.
// The actual refresh is deferred to the next periodic tick (stripRefreshInterval)
// to avoid calling refreshSlots/refreshSwarmSlots on every streaming token.
func (f *subAgentFollowState) markStripDirty() {
	f.stripDirty = true
}

// refreshStripIfNeeded performs the strip refresh only if enough time has
// elapsed since the last refresh (stripRefreshInterval). This is called
// from handleSubAgentUpdateMsg and handleSubAgentFollowRefreshMsg.
// Returns true if a refresh happened.
func (f *subAgentFollowState) refreshStripIfNeeded(saMgr *subagent.Manager, swMgr *swarm.Manager) bool {
	if !f.stripDirty {
		return false
	}
	now := time.Now()
	if !f.lastStripRefresh.IsZero() && now.Sub(f.lastStripRefresh) < stripRefreshInterval {
		return false // still throttled
	}
	f.refreshSlots(saMgr)
	f.refreshSwarmSlots(swMgr)
	f.lastStripRefresh = now
	f.stripDirty = false
	return true
}

// isActive returns true if we're following a specific agent.
func (f *subAgentFollowState) isActive() bool {
	return f.activeID != ""
}

// activate enters follow mode for the given slot index.
func (f *subAgentFollowState) activate(index int) {
	if index < 0 || index >= len(f.slots) {
		return
	}
	f.activeID = f.slots[index].ID
}

// deactivate exits follow mode and returns the previously active ID.
func (f *subAgentFollowState) deactivate() string {
	prev := f.activeID
	f.activeID = ""
	return prev
}

// currentSlotIndex returns the index of the currently active slot, or -1 if not active.
func (f *subAgentFollowState) currentSlotIndex() int {
	if !f.isActive() {
		return -1
	}
	for i, slot := range f.slots {
		if slot.ID == f.activeID {
			return i
		}
	}
	return -1
}

// autoReturnIfNeeded is a no-op — user must press Esc to exit follow mode.
func (f *subAgentFollowState) autoReturnIfNeeded(mgr *subagent.Manager) (returnedID string) {
	if f.activeID == "" || mgr == nil {
		return ""
	}
	_, ok := mgr.Snapshot(f.activeID)
	if !ok {
		id := f.activeID
		f.activeID = ""
		return id
	}
	return ""
}

func (f *subAgentFollowState) getOrCreateView(agentID string, width, height int) *followViewEntry {
	if f.views == nil {
		f.views = make(map[string]*followViewEntry)
	}
	entry, ok := f.views[agentID]
	if !ok {
		entry = &followViewEntry{
			list: chat.NewList(width, height),
		}
		f.views[agentID] = entry
	}
	return entry
}

// ---------------------------------------------------------------------------
// Follow list building (unified for sub-agents and teammates)
// ---------------------------------------------------------------------------

// followEventData is the common data needed to build a follow list.
type followEventData struct {
	ID            string
	Name          string
	Task          string
	Status        string // "running", "completed", "failed", "idle", "working"
	Events        []followEvent
	EventsDropped int
}

// followEvent is a unified event type for both sub-agent and teammate events.
type followEvent struct {
	Type            int // 0=text, 1=toolCall, 2=toolResult, 3=error
	Text            string
	ToolID          string
	ToolName        string
	ToolArgs        string
	ToolDisplayName string
	ToolDetail      string
	Result          string
	IsError         bool
}

const (
	followEventText       = 0
	followEventToolCall   = 1
	followEventToolResult = 2
	followEventError      = 3
)

// buildFollowList builds a chat list from followEventData.
// This is the unified renderer for both sub-agents and teammates.
func buildFollowList(data followEventData, list *chat.List, styles chat.Styles) {
	list.SetItems(nil)

	// Header: name + status
	name := data.Name
	if name == "" {
		name = shortID(data.ID)
	}
	header := fmt.Sprintf("%s  ·  %s", name, data.Task)
	switch data.Status {
	case "completed":
		header += "  ✓ completed"
	case "failed":
		header += "  ✗ failed"
	case "running", "working":
		header += "  ● running"
	}
	list.Append(chat.NewSystemItem("header", header, styles))

	if data.EventsDropped > 0 {
		list.Append(chat.NewSystemItem("trunc",
			fmt.Sprintf("⚠ Early output truncated (%d events dropped)", data.EventsDropped),
			styles,
		))
	}

	// Text flush helper
	flushText := func(buf *strings.Builder) {
		text := buf.String()
		buf.Reset()
		if strings.TrimSpace(text) == "" {
			return
		}
		ai := chat.NewAssistantItem("", styles)
		ai.SetText(text)
		// Follow panel shows snapshots (complete text, not streaming).
		// Mark as finished so Render() uses markdown.Render() instead of
		// renderStreamingMarkdown(), which can mangle whitespace.
		ai.SetFinished()
		list.Append(ai)
	}

	toolCalls := make(map[string]int)
	toolCallCount := make(map[string]int)
	toolResultCount := make(map[string]int)
	var textBuf strings.Builder

	for _, ev := range data.Events {
		switch ev.Type {
		case followEventText:
			textBuf.WriteString(ev.Text)

		case followEventToolCall:
			flushText(&textBuf)
			toolCallCount[ev.ToolName]++
			key := ev.ToolID
			if key == "" {
				key = fmt.Sprintf("%s-%d", ev.ToolName, toolCallCount[ev.ToolName])
			}
			toolCalls[key] = list.Len()
			present := describeTool("en", ev.ToolName, ev.ToolArgs)
			if ev.ToolDisplayName != "" {
				present.DisplayName = ev.ToolDisplayName
			}
			if ev.ToolDetail != "" {
				present.Detail = ev.ToolDetail
			}
			item := chat.NewToolItem(
				key,
				chat.ToolContext{
					ToolName:    ev.ToolName,
					DisplayName: present.DisplayName,
					Detail:      present.Detail,
					RawArgs:     ev.ToolArgs,
					Lang:        "en",
				},
				chat.StatusRunning,
				styles,
			)
			list.Append(item)

		case followEventToolResult:
			flushText(&textBuf)
			status := chat.StatusSuccess
			if ev.IsError {
				status = chat.StatusError
			}
			key := ev.ToolID
			if key == "" {
				// Match tool results to calls sequentially when no stable tool ID exists.
				toolResultCount[ev.ToolName]++
				key = fmt.Sprintf("%s-%d", ev.ToolName, toolResultCount[ev.ToolName])
			}
			if idx, ok := toolCalls[key]; ok {
				existing := list.ItemAt(idx)
				if setter, ok := existing.(interface{ SetResult(string, bool) }); ok {
					setter.SetResult(ev.Result, ev.IsError)
				}
				if statusSetter, ok := existing.(interface{ SetStatus(chat.ToolStatus) }); ok {
					statusSetter.SetStatus(status)
				}
			} else {
				item := chat.NewGenericToolItem("result", ev.ToolName, status, util.Truncate(ev.Result, 200), styles)
				list.Append(item)
			}

		case followEventError:
			flushText(&textBuf)
			list.Append(chat.NewSystemItem("error", "Error: "+ev.Text, styles))
		}
	}
	flushText(&textBuf)
}

// subagentSnapshotToFollowData converts a subagent.Snapshot to followEventData.
func subagentSnapshotToFollowData(snap subagent.Snapshot) followEventData {
	events := make([]followEvent, len(snap.Events))
	for i, ev := range snap.Events {
		var t int
		switch ev.Type {
		case subagent.AgentEventText:
			t = followEventText
		case subagent.AgentEventToolCall:
			t = followEventToolCall
		case subagent.AgentEventToolResult:
			t = followEventToolResult
		case subagent.AgentEventError:
			t = followEventError
		}
		events[i] = followEvent{
			Type:            t,
			Text:            ev.Text,
			ToolID:          ev.ToolID,
			ToolName:        ev.ToolName,
			ToolArgs:        ev.ToolArgs,
			ToolDisplayName: ev.ToolDisplayName,
			ToolDetail:      ev.ToolDetail,
			Result:          ev.Result,
			IsError:         ev.IsError,
		}
	}
	status := string(snap.Status)
	return followEventData{
		ID:            snap.ID,
		Name:          snap.Name,
		Task:          snap.DisplayTask,
		Status:        status,
		Events:        events,
		EventsDropped: snap.EventsDropped,
	}
}

// teammateSnapshotToFollowData converts a swarm.TeammateSnapshot to followEventData.
func teammateSnapshotToFollowData(snap swarm.TeammateSnapshot) followEventData {
	events := make([]followEvent, len(snap.Events))
	for i, ev := range snap.Events {
		var t int
		switch ev.Type {
		case swarm.TeammateEventText:
			t = followEventText
		case swarm.TeammateEventToolCall:
			t = followEventToolCall
		case swarm.TeammateEventToolResult:
			t = followEventToolResult
		case swarm.TeammateEventError:
			t = followEventError
		}
		events[i] = followEvent{
			Type:     t,
			Text:     ev.Text,
			ToolID:   ev.ToolID,
			ToolName: ev.ToolName,
			ToolArgs: ev.ToolArgs,
			Result:   ev.Result,
			IsError:  ev.IsError,
		}
	}
	status := string(snap.Status)
	if status == "working" {
		status = "running"
	}
	return followEventData{
		ID:            snap.ID,
		Name:          snap.Name,
		Task:          snap.CurrentTask,
		Status:        status,
		Events:        events,
		EventsDropped: snap.EventsDropped,
	}
}

// ---------------------------------------------------------------------------
// rebuildActiveView: called from Update(), NOT from View()
// ---------------------------------------------------------------------------

// rebuildActiveView rebuilds the follow list for the currently active slot.
// Must be called from Update(), NOT from View().
func (f *subAgentFollowState) rebuildActiveView(saMgr *subagent.Manager, swMgr *swarm.Manager, styles chat.Styles) {
	if !f.isActive() {
		return
	}
	entry := f.getOrCreateView(f.activeID, 0, 0) // width/height set later in View()

	// Try sub-agent first
	if saMgr != nil {
		if snap, ok := saMgr.Snapshot(f.activeID); ok {
			buildFollowList(subagentSnapshotToFollowData(snap), entry.list, styles)
			f.markRebuilt(f.activeID)
			return
		}
	}
	// Try swarm teammate
	if swMgr != nil {
		if snap, ok := swMgr.TeammateSnapshot(f.activeID); ok {
			buildFollowList(teammateSnapshotToFollowData(snap), entry.list, styles)
			f.markRebuilt(f.activeID)
		}
	}
}

// ---------------------------------------------------------------------------
// Throttle & cleanup
// ---------------------------------------------------------------------------

// shouldRebuild checks throttle and dirty state.
func (f *subAgentFollowState) shouldRebuild(agentID string) bool {
	if f.dirty == nil || !f.dirty[agentID] {
		return false
	}
	if f.lastRebuild == nil {
		return true
	}
	last, ok := f.lastRebuild[agentID]
	if !ok {
		return true
	}
	return time.Since(last) >= 100*time.Millisecond
}

// markRebuilt clears the dirty flag and records the rebuild time.
func (f *subAgentFollowState) markRebuilt(agentID string) {
	if f.dirty != nil {
		delete(f.dirty, agentID)
	}
	if f.lastRebuild == nil {
		f.lastRebuild = make(map[string]time.Time)
	}
	f.lastRebuild[agentID] = time.Now()
}

// cleanup removes cached views for agents/teammates that no longer exist.
func (f *subAgentFollowState) cleanup(saMgr *subagent.Manager, swMgr *swarm.Manager) {
	if f.views == nil || len(f.views) == 0 {
		return
	}
	active := make(map[string]bool)
	if saMgr != nil {
		for _, sa := range saMgr.List() {
			active[sa.ID] = true
		}
	}
	if swMgr != nil {
		for _, ts := range swMgr.ListTeams() {
			for _, tm := range ts.Teammates {
				active[tm.ID] = true
			}
		}
	}
	for id := range f.views {
		if !active[id] {
			delete(f.views, id)
			if f.dirty != nil {
				delete(f.dirty, id)
			}
			if f.lastRebuild != nil {
				delete(f.lastRebuild, id)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Strip rendering
// ---------------------------------------------------------------------------

func (m *Model) renderSubAgentFollowStrip() string {
	if m.subAgentFollow.slots == nil || len(m.subAgentFollow.slots) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("  ")

	maxShow := 5
	if len(m.subAgentFollow.slots) < maxShow {
		maxShow = len(m.subAgentFollow.slots)
	}

	for i := 0; i < maxShow; i++ {
		slot := m.subAgentFollow.slots[i]

		label := slot.Name
		if label == "" {
			label = shortID(slot.ID)
		}

		activity := slot.Phase
		if activity == "" {
			if slot.Terminal {
				activity = m.t("follow.status_done")
			} else {
				activity = m.t("follow.status_running")
			}
		}

		// Kind prefix
		prefix := ""
		if slot.Kind == followSlotTeammate {
			prefix = "👤 "
		}

		chip := fmt.Sprintf("%s%s · %s", prefix, label, activity)

		if m.subAgentFollow.activeID == slot.ID {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
			chip = fmt.Sprintf("▶ %s", chip)
			b.WriteString(style.Render(chip))
		} else {
			b.WriteString(chip)
		}

		if i < maxShow-1 {
			b.WriteString("  │  ")
		}
	}

	if len(m.subAgentFollow.slots) > maxShow {
		b.WriteString(fmt.Sprintf(m.t("follow.more"), len(m.subAgentFollow.slots)-maxShow))
	}

	b.WriteString(m.t("follow.hint"))
	return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(b.String())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// hasTerminalSlots returns true if any slot is in a terminal state (completed/failed/cancelled/idle).
// Used to decide whether the grace-period cleanup timer should be running.
func (f *subAgentFollowState) hasTerminalSlots() bool {
	for _, s := range f.slots {
		if s.Terminal {
			return true
		}
	}
	return false
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ---------------------------------------------------------------------------
// Per-ID text throttle map (used by repl.go for swarm teammate_text events)
// ---------------------------------------------------------------------------

// textThrottleMap provides per-ID time-based throttling. It is used to
// prevent high-frequency streaming text events (one per LLM token) from
// flooding Bubble Tea's event loop.
type textThrottleMap struct {
	mu    sync.Mutex
	last  map[string]time.Time
	delay time.Duration
}

// newTextThrottleMap creates a throttle map with the given minimum interval.
func newTextThrottleMap(delay time.Duration) *textThrottleMap {
	return &textThrottleMap{last: make(map[string]time.Time), delay: delay}
}

// Allow returns true if enough time has elapsed since the last Allow for this
// id. It updates the timestamp atomically.
func (t *textThrottleMap) Allow(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	last := t.last[id]
	if !last.IsZero() && now.Sub(last) < t.delay {
		return false
	}
	t.last[id] = now
	return true
}

// Clear removes the throttle state for a given id (e.g., on completion).
func (t *textThrottleMap) Clear(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, id)
}
