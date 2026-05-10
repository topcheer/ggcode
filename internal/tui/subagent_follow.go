package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
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

// subAgentFollowState tracks the TUI follow-mode for sub-agents AND swarm teammates.
// Ctrl+N cycles through all slots regardless of kind.
type subAgentFollowState struct {
	activeID    string                      // agent/teammate ID being followed ("" = main view)
	slots       []followSlot                // ordered list of all followable agents
	views       map[string]*followViewEntry // cached chat lists per agent
	dirty       map[string]bool             // which views need rebuild
	lastRebuild map[string]time.Time        // throttle: last rebuild time per view
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
	if saMgr != nil {
		agents := saMgr.List()
		slices.SortFunc(agents, func(a, b *subagent.SubAgent) int {
			return cmp.Compare(a.ID, b.ID)
		})
		now := time.Now()
		for _, sa := range agents {
			snap, _ := saMgr.Snapshot(sa.ID)
			isTerminal := snap.Status == subagent.StatusCompleted || snap.Status == subagent.StatusFailed || snap.Status == subagent.StatusCancelled
			if isTerminal {
				// Keep in strip during grace period so user can see the result
				if !snap.EndedAt.IsZero() && now.Sub(snap.EndedAt) < subAgentGracePeriod {
					newSlots = append(newSlots, followSlot{
						ID:       snap.ID,
						Name:     snap.Name,
						Kind:     followSlotSubAgent,
						Phase:    "done",
						Terminal: true,
					})
				}
				continue
			}
			newSlots = append(newSlots, followSlot{
				ID:       snap.ID,
				Name:     snap.Name,
				Kind:     followSlotSubAgent,
				Phase:    snap.CurrentPhase,
				Terminal: false,
			})
		}
	}

	f.slots = newSlots
}

// refreshSwarmSlots appends swarm teammate slots.
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

	for _, ts := range swMgr.ListTeams() {
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
	Type     int // 0=text, 1=toolCall, 2=toolResult, 3=error
	Text     string
	ToolName string
	ToolArgs string
	Result   string
	IsError  bool
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
		list.Append(ai)
	}

	toolCalls := make(map[string]int)
	toolCallCount := make(map[string]int)
	var textBuf strings.Builder

	for _, ev := range data.Events {
		switch ev.Type {
		case followEventText:
			textBuf.WriteString(ev.Text)

		case followEventToolCall:
			flushText(&textBuf)
			toolCalls[ev.ToolName] = list.Len()
			toolCallCount[ev.ToolName]++
			present := describeTool("en", ev.ToolName, ev.ToolArgs)
			item := chat.NewToolItem(
				fmt.Sprintf("%s-%d", ev.ToolName, toolCallCount[ev.ToolName]),
				chat.ToolContext{
					ToolName:    ev.ToolName,
					DisplayName: present.DisplayName,
					Detail:      present.Detail,
					RawArgs:     ev.ToolArgs,
					Lang:        "en",
				},
				chat.StatusPending,
				styles,
			)
			list.Append(item)

		case followEventToolResult:
			flushText(&textBuf)
			status := chat.StatusSuccess
			if ev.IsError {
				status = chat.StatusError
			}
			if idx, ok := toolCalls[ev.ToolName]; ok {
				existing := list.ItemAt(idx)
				if setter, ok := existing.(interface{ SetResult(string, bool) }); ok {
					setter.SetResult(ev.Result, ev.IsError)
				}
			} else {
				item := chat.NewGenericToolItem("result", ev.ToolName, status, truncate(ev.Result, 200), styles)
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
			Type:     t,
			Text:     ev.Text,
			ToolName: ev.ToolName,
			ToolArgs: ev.ToolArgs,
			Result:   ev.Result,
			IsError:  ev.IsError,
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

// cleanup removes cached views for agents that no longer exist.
func (f *subAgentFollowState) cleanup(saMgr *subagent.Manager) {
	if f.views == nil || len(f.views) == 0 {
		return
	}
	active := make(map[string]bool)
	if saMgr != nil {
		for _, sa := range saMgr.List() {
			active[sa.ID] = true
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
				activity = "done"
			} else {
				activity = "running"
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
		b.WriteString(fmt.Sprintf("  +%d more", len(m.subAgentFollow.slots)-maxShow))
	}

	b.WriteString("   ↑↓←→ switch  Esc close")
	return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(b.String())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
