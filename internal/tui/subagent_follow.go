package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/subagent"
)

// ---------------------------------------------------------------------------
// Follow state
// ---------------------------------------------------------------------------

// subAgentFollowState tracks the TUI follow-mode for sub-agents.
type subAgentFollowState struct {
	activeID    string                      // agent ID being followed ("" = main view)
	slots       []subAgentFollowSlot        // ordered list of running sub-agents
	views       map[string]*followViewEntry // cached chat lists per agent
	dirty       map[string]bool             // which views need rebuild
	lastRebuild map[string]time.Time        // throttle: last rebuild time per view
}

type subAgentFollowSlot struct {
	ID          string
	DisplayTask string
	Phase       string
	Status      subagent.Status
}

type followViewEntry struct {
	list    *chat.List
	dropped int
}

// ---------------------------------------------------------------------------
// Slot management
// ---------------------------------------------------------------------------

// refreshSlots rebuilds the slot list from the manager.
func (f *subAgentFollowState) refreshSlots(mgr *subagent.Manager) {
	if mgr == nil {
		f.slots = nil
		return
	}
	agents := mgr.List()
	newSlots := make([]subAgentFollowSlot, 0, len(agents))
	for _, sa := range agents {
		snap, _ := mgr.Snapshot(sa.ID)
		newSlots = append(newSlots, subAgentFollowSlot{
			ID:          snap.ID,
			DisplayTask: snap.DisplayTask,
			Phase:       snap.CurrentPhase,
			Status:      snap.Status,
		})
	}
	f.slots = newSlots
}

// markDirty marks a view as needing rebuild.
func (f *subAgentFollowState) markDirty(agentID string) {
	if f.dirty == nil {
		f.dirty = make(map[string]bool)
	}
	f.dirty[agentID] = true
}

// isActive returns true if we're following a specific sub-agent.
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

// deactivate exits follow mode and returns the previously active agent ID.
func (f *subAgentFollowState) deactivate() string {
	prev := f.activeID
	f.activeID = ""
	return prev
}

// autoReturnIfNeeded checks if the active sub-agent finished and auto-returns.
func (f *subAgentFollowState) autoReturnIfNeeded(mgr *subagent.Manager) (returnedID string) {
	if f.activeID == "" || mgr == nil {
		return ""
	}
	snap, ok := mgr.Snapshot(f.activeID)
	if !ok {
		// Agent removed entirely
		id := f.activeID
		f.activeID = ""
		return id
	}
	if snap.Status == subagent.StatusCompleted || snap.Status == subagent.StatusFailed || snap.Status == subagent.StatusCancelled {
		id := f.activeID
		f.activeID = ""
		return id
	}
	return ""
}

// ---------------------------------------------------------------------------
// View building
// ---------------------------------------------------------------------------

// getOrCreateView returns the cached view for an agent, creating if needed.
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

// buildView rebuilds the chat list for an agent from its snapshot events.
func buildSubAgentFollowList(snap subagent.Snapshot, list *chat.List, styles chat.Styles) {
	list.SetItems(nil)

	// Header: task name + status
	header := fmt.Sprintf("Sub-agent %s  ·  %s", shortID(snap.ID), snap.DisplayTask)
	if snap.Status == subagent.StatusCompleted {
		header += "  ✓ completed"
	} else if snap.Status == subagent.StatusFailed {
		header += "  ✗ failed"
	} else if snap.Status == subagent.StatusRunning {
		header += "  ● running"
	}
	list.Append(chat.NewSystemItem("sa-header", header, styles))

	// Truncation notice
	if snap.EventsDropped > 0 {
		list.Append(chat.NewSystemItem("sa-trunc",
			fmt.Sprintf("⚠ Early output truncated (%d events dropped)", snap.EventsDropped),
			styles,
		))
	}

	// Build a map to pair tool calls with results
	toolCalls := make(map[string]int)     // toolName → list index
	toolCallCount := make(map[string]int) // toolName → count for unique keys
	for _, ev := range snap.Events {
		switch ev.Type {
		case subagent.AgentEventText:
			if strings.TrimSpace(ev.Text) != "" {
				ai := chat.NewAssistantItem("", styles)
				ai.SetText(ev.Text)
				list.Append(ai)
			}

		case subagent.AgentEventToolCall:
			toolCalls[ev.ToolName] = list.Len()
			toolCallCount[ev.ToolName]++
			item := chat.NewGenericToolItem(
				fmt.Sprintf("%s-%d", ev.ToolName, toolCallCount[ev.ToolName]),
				ev.ToolName,
				chat.StatusPending,
				truncate(ev.ToolArgs, 120),
				styles,
			)
			list.Append(item)

		case subagent.AgentEventToolResult:
			status := chat.StatusSuccess
			if ev.IsError {
				status = chat.StatusError
			}
			if idx, ok := toolCalls[ev.ToolName]; ok {
				// Update existing tool item
				existing := list.ItemAt(idx)
				if setter, ok := existing.(interface{ SetResult(string, bool) }); ok {
					setter.SetResult(ev.Result, ev.IsError)
				}
			} else {
				// Orphan result — add as standalone
				item := chat.NewGenericToolItem(
					"result",
					ev.ToolName,
					status,
					truncate(ev.Result, 200),
					styles,
				)
				list.Append(item)
			}

		case subagent.AgentEventError:
			list.Append(chat.NewSystemItem("sa-error", "Error: "+ev.Text, styles))
		}
	}
}

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
	// Throttle: at least 100ms between rebuilds
	return time.Since(last) >= 100*time.Millisecond
}

// markRebuilt records that a view was rebuilt.
func (f *subAgentFollowState) markRebuilt(agentID string) {
	if f.dirty != nil {
		delete(f.dirty, agentID)
	}
	if f.lastRebuild == nil {
		f.lastRebuild = make(map[string]time.Time)
	}
	f.lastRebuild[agentID] = time.Now()
}

// cleanup removes views for agents that no longer exist.
func (f *subAgentFollowState) cleanup(mgr *subagent.Manager) {
	if mgr == nil || f.views == nil {
		return
	}
	active := make(map[string]bool)
	for _, slot := range f.slots {
		active[slot.ID] = true
	}
	// Also keep recently completed agents for a short time
	for id := range f.views {
		if !active[id] {
			_, exists := mgr.Get(id)
			if !exists {
				delete(f.views, id)
				delete(f.dirty, id)
				delete(f.lastRebuild, id)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Follow strip rendering
// ---------------------------------------------------------------------------

// renderSubAgentFollowStrip renders the strip above the composer showing active sub-agents.
func (m *Model) renderSubAgentFollowStrip() string {
	if m.subAgentFollow.slots == nil || len(m.subAgentFollow.slots) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("  ")

	slotKeys := []string{"!", "@", "#", "$", "%", "^", "&", "*", "("}

	maxShow := 9
	if len(m.subAgentFollow.slots) < maxShow {
		maxShow = len(m.subAgentFollow.slots)
	}

	for i := 0; i < maxShow; i++ {
		slot := m.subAgentFollow.slots[i]
		key := slotKeys[i]

		// Highlight if this is the active follow
		label := slot.DisplayTask
		if label == "" {
			label = shortID(slot.ID)
		}
		if len(label) > 20 {
			label = label[:17] + "..."
		}

		// Activity hint
		activity := slot.Phase
		if activity == "" {
			activity = string(slot.Status)
		}

		chip := fmt.Sprintf("%s %s · %s", key, label, activity)

		// Active slot gets accent color
		if m.subAgentFollow.activeID == slot.ID {
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
			b.WriteString(style.Render(chip))
		} else {
			b.WriteString(chip)
		}

		if i < maxShow-1 {
			b.WriteString("   ")
		}
	}

	if len(m.subAgentFollow.slots) > maxShow {
		b.WriteString(fmt.Sprintf("   +%d more", len(m.subAgentFollow.slots)-maxShow))
	}

	b.WriteString("   Esc close")
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
