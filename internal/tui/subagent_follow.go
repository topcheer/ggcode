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
	ID     string
	Name   string
	Phase  string
	Status subagent.Status
}

type followViewEntry struct {
	list    *chat.List
	dropped int
}

// ---------------------------------------------------------------------------
// Slot management
// ---------------------------------------------------------------------------

// refreshSlots rebuilds the slot list from the manager.
// Slots are sorted by ID for stable ordering across refreshes.
func (f *subAgentFollowState) refreshSlots(mgr *subagent.Manager) {
	if mgr == nil {
		f.slots = nil
		return
	}
	agents := mgr.List()
	// Sort by ID for stable ordering (IDs are sa-1, sa-2, ...)
	slices.SortFunc(agents, func(a, b *subagent.SubAgent) int {
		return cmp.Compare(a.ID, b.ID)
	})
	newSlots := make([]subAgentFollowSlot, 0, len(agents))
	for _, sa := range agents {
		snap, _ := mgr.Snapshot(sa.ID)
		newSlots = append(newSlots, subAgentFollowSlot{
			ID:     snap.ID,
			Name:   snap.Name,
			Phase:  snap.CurrentPhase,
			Status: snap.Status,
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
// Skips terminal-status (completed/failed/cancelled) agents.
func (f *subAgentFollowState) activate(index int) {
	if index < 0 || index >= len(f.slots) {
		return
	}
	if isTerminalStatus(f.slots[index].Status) {
		// Find next non-terminal slot
		for i := 1; i <= len(f.slots); i++ {
			idx := (index + i) % len(f.slots)
			if !isTerminalStatus(f.slots[idx].Status) {
				f.activeID = f.slots[idx].ID
				return
			}
		}
		// All terminal — activate anyway so user can see the completed view
		f.activeID = f.slots[index].ID
		return
	}
	f.activeID = f.slots[index].ID
}

// isTerminalStatus returns true if the agent has finished.
func isTerminalStatus(s subagent.Status) bool {
	return s == subagent.StatusCompleted || s == subagent.StatusFailed || s == subagent.StatusCancelled
}

// deactivate exits follow mode and returns the previously active agent ID.
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

// autoReturnIfNeeded checks if the active sub-agent finished and auto-returns.
// This is now only used for agents that were running when follow mode was entered.
func (f *subAgentFollowState) autoReturnIfNeeded(mgr *subagent.Manager) (returnedID string) {
	if f.activeID == "" || mgr == nil {
		return ""
	}
	_, ok := mgr.Snapshot(f.activeID)
	if !ok {
		// Agent removed entirely
		id := f.activeID
		f.activeID = ""
		return id
	}
	// Don't auto-return — let the user view completed agents and press Esc manually.
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

// buildSubAgentFollowList rebuilds the chat list for an agent from its snapshot events.
// Consecutive AgentEventText events are merged into a single AssistantItem so that
// streaming chunks render as one coherent message with proper markdown formatting.
func buildSubAgentFollowList(snap subagent.Snapshot, list *chat.List, styles chat.Styles) {
	list.SetItems(nil)

	// Header: name + status
	name := snap.Name
	if name == "" {
		name = shortID(snap.ID)
	}
	header := fmt.Sprintf("Sub-agent %s  ·  %s", name, snap.DisplayTask)
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

	// Helper: flush accumulated text as a single AssistantItem.
	// This merges streaming chunks into one coherent block for proper markdown rendering.
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

	// Build a map to pair tool calls with results
	toolCalls := make(map[string]int)     // toolName → list index
	toolCallCount := make(map[string]int) // toolName → count for unique keys
	var textBuf strings.Builder

	for _, ev := range snap.Events {
		switch ev.Type {
		case subagent.AgentEventText:
			// Accumulate text chunks; flush on non-text events
			textBuf.WriteString(ev.Text)

		case subagent.AgentEventToolCall:
			// Flush any pending text before adding a tool item
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

		case subagent.AgentEventToolResult:
			// Flush any pending text before handling result
			flushText(&textBuf)

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
			// Flush any pending text before adding error
			flushText(&textBuf)

			list.Append(chat.NewSystemItem("sa-error", "Error: "+ev.Text, styles))
		}
	}

	// Flush any remaining text
	flushText(&textBuf)
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

	maxShow := 5
	if len(m.subAgentFollow.slots) < maxShow {
		maxShow = len(m.subAgentFollow.slots)
	}

	for i := 0; i < maxShow; i++ {
		slot := m.subAgentFollow.slots[i]

		// Use Name as label
		label := slot.Name
		if label == "" {
			label = shortID(slot.ID)
		}

		// Activity hint
		activity := slot.Phase
		if activity == "" {
			activity = string(slot.Status)
		}

		chip := fmt.Sprintf("%s · %s", label, activity)

		// Active slot gets accent color
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

	// Control hints
	b.WriteString("   Ctrl+N next  Ctrl+P prev  Esc close")
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
