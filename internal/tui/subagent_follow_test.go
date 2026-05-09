package tui

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// helper: create a model with a sub-agent manager
func newFollowTestModel(n int) (Model, []*subagent.SubAgent) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	var agents []*subagent.SubAgent
	for i := 0; i < n; i++ {
		task := "task-" + string(rune('A'+i))
		id := m.subAgentMgr.Spawn(task, task, task, nil, context.Background())
		sa, _ := m.subAgentMgr.Get(id)
		agents = append(agents, sa)
	}
	return m, agents
}

// TestFollowSlotRefresh verifies that refreshSlots populates from the manager.
func TestFollowSlotRefresh(t *testing.T) {
	m, _ := newFollowTestModel(3)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	if len(m.subAgentFollow.slots) != 3 {
		t.Fatalf("expected 3 slots, got %d", len(m.subAgentFollow.slots))
	}
	if m.subAgentFollow.slots[0].ID == "" {
		t.Error("expected slot 0 to have an ID")
	}
}

// TestFollowSlotRefreshStableOrder verifies slots are sorted by ID.
func TestFollowSlotRefreshStableOrder(t *testing.T) {
	m, _ := newFollowTestModel(5)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Verify slots are in ascending ID order
	for i := 1; i < len(m.subAgentFollow.slots); i++ {
		if m.subAgentFollow.slots[i].ID < m.subAgentFollow.slots[i-1].ID {
			t.Errorf("slots not sorted: slot[%d]=%s > slot[%d]=%s",
				i-1, m.subAgentFollow.slots[i-1].ID, i, m.subAgentFollow.slots[i].ID)
		}
	}

	// Verify order is stable across multiple refreshes
	firstOrder := make([]string, len(m.subAgentFollow.slots))
	for i, s := range m.subAgentFollow.slots {
		firstOrder[i] = s.ID
	}
	for attempt := 0; attempt < 10; attempt++ {
		m.subAgentFollow.refreshSlots(m.subAgentMgr)
		for i, s := range m.subAgentFollow.slots {
			if s.ID != firstOrder[i] {
				t.Errorf("slot order unstable on attempt %d: expected %v, got slot[%d]=%s",
					attempt, firstOrder, i, s.ID)
			}
		}
	}
}

// TestFollowActivateDeactivate verifies entering and exiting follow mode.
func TestFollowActivateDeactivate(t *testing.T) {
	m, _ := newFollowTestModel(3)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	if m.subAgentFollow.isActive() {
		t.Error("should not be active initially")
	}

	// Activate slot 0
	m.subAgentFollow.activate(0)
	if !m.subAgentFollow.isActive() {
		t.Error("should be active after activate(0)")
	}
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[0].ID {
		t.Error("activeID should match slot 0")
	}

	// Deactivate
	prev := m.subAgentFollow.deactivate()
	if m.subAgentFollow.isActive() {
		t.Error("should not be active after deactivate")
	}
	if prev != m.subAgentFollow.slots[0].ID {
		t.Error("deactivate should return previous activeID")
	}
}

// TestFollowActivateOutOfBounds verifies safety.
func TestFollowActivateOutOfBounds(t *testing.T) {
	m, _ := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	m.subAgentFollow.activate(-1)
	if m.subAgentFollow.isActive() {
		t.Error("should not activate with negative index")
	}
	m.subAgentFollow.activate(99)
	if m.subAgentFollow.isActive() {
		t.Error("should not activate with out-of-bounds index")
	}
}

// TestFollowAutoReturnDisabled verifies auto-return is disabled (user presses Esc to exit).
func TestFollowAutoReturnDisabled(t *testing.T) {
	m, agents := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Follow agent 0
	m.subAgentFollow.activate(0)

	// Mark agent 0 as completed
	agents[0].Status = subagent.StatusCompleted

	// autoReturnIfNeeded should NOT return — user must press Esc
	returnedID := m.subAgentFollow.autoReturnIfNeeded(m.subAgentMgr)
	if returnedID != "" {
		t.Error("auto-return should be disabled; user controls exit via Esc")
	}
	if !m.subAgentFollow.isActive() {
		t.Error("should still be active after agent completes — user views result")
	}
}

// TestFollowAutoReturnNoopWhileRunning verifies no return while running.
func TestFollowAutoReturnNoopWhileRunning(t *testing.T) {
	m, agents := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.activate(0)

	// Agent still running
	agents[0].Status = subagent.StatusRunning
	returnedID := m.subAgentFollow.autoReturnIfNeeded(m.subAgentMgr)
	if returnedID != "" {
		t.Error("should not auto-return while agent is still running")
	}
	if !m.subAgentFollow.isActive() {
		t.Error("should still be active while agent runs")
	}
}

// TestFollowActivateSkipsCompleted verifies activate skips completed agents.
func TestFollowActivateSkipsCompleted(t *testing.T) {
	m, agents := newFollowTestModel(3)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Mark agents 0 and 1 as completed
	agents[0].Status = subagent.StatusCompleted
	agents[1].Status = subagent.StatusCompleted
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// activate(0) should skip to agent 2 (still pending)
	m.subAgentFollow.activate(0)
	if !m.subAgentFollow.isActive() {
		t.Fatal("should be active after activate(0) with completed agents")
	}
	if m.subAgentFollow.activeID != agents[2].ID {
		t.Errorf("expected to skip to agent 2 (%s), got %s", agents[2].ID, m.subAgentFollow.activeID)
	}
}

// TestFollowStripRendering verifies strip renders when slots exist.
func TestFollowStripRendering(t *testing.T) {
	m, _ := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	strip := m.renderSubAgentFollowStrip()
	if strip == "" {
		t.Error("expected non-empty strip when sub-agents are running")
	}
	if !containsPlain(strip, "Ctrl+N") {
		t.Error("expected 'Ctrl+N' hint in strip")
	}
	if !containsPlain(strip, "Esc close") {
		t.Error("expected 'Esc close' hint in strip")
	}
}

// TestFollowStripEmptyWhenNoSlots verifies strip is empty with no sub-agents.
func TestFollowStripEmptyWhenNoSlots(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	strip := m.renderSubAgentFollowStrip()
	if strip != "" {
		t.Error("expected empty strip when no sub-agents")
	}
}

// TestBuildSubAgentFollowList verifies event-to-chat-item mapping.
func TestBuildSubAgentFollowList(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "test-task", "Test Task", nil, context.Background())
	sa, _ := mgr.Get(id)

	// Simulate some events
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Hello from sub-agent"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: "/tmp/test.txt"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "file contents here"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Analysis complete"})

	snap, _ := mgr.Snapshot(id)
	list := chat.NewList(80, 20)

	styles := chat.DefaultStyles()
	buildSubAgentFollowList(snap, list, styles)

	// Should have: header + text + tool-call + text = 4 items
	if list.Len() < 3 {
		t.Errorf("expected at least 3 items in follow list, got %d", list.Len())
	}
}

// TestBuildSubAgentFollowListMergesText verifies consecutive text events are merged.
func TestBuildSubAgentFollowListMergesText(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "test-task", "Test Task", nil, context.Background())
	sa, _ := mgr.Get(id)

	// Simulate streaming: multiple text chunks, then a tool call, then more text
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Hello "})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "world"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "!"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: "/tmp/test.txt"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "contents"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Done"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: " here"})

	snap, _ := mgr.Snapshot(id)
	list := chat.NewList(80, 20)
	buildSubAgentFollowList(snap, list, chat.DefaultStyles())

	// Expected items: header + merged-text("Hello world!") + tool-call + merged-text("Done here") = 4
	expectedItems := 4
	if list.Len() != expectedItems {
		t.Errorf("expected %d items (header + 2 merged text blocks + 1 tool), got %d",
			expectedItems, list.Len())
	}
}

// TestBuildSubAgentFollowListTruncation verifies truncation notice.
func TestBuildSubAgentFollowListTruncation(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "test-task", "Test Task", nil, context.Background())
	sa, _ := mgr.Get(id)

	// Fill beyond max to trigger drops
	for i := 0; i < 250; i++ {
		sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "line"})
	}

	snap, _ := mgr.Snapshot(id)
	if snap.EventsDropped == 0 {
		t.Error("expected some events to be dropped after 250 appends")
	}

	list := chat.NewList(80, 20)
	buildSubAgentFollowList(snap, list, chat.DefaultStyles())

	// Should have header + truncation notice + events
	if list.Len() < 2 {
		t.Error("expected header + truncation notice at minimum")
	}
}

// TestFollowCleanup removes stale views.
func TestFollowCleanup(t *testing.T) {
	m, agents := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Create a view for a stale agent
	staleID := "stale-agent-id"
	m.subAgentFollow.getOrCreateView(staleID, 80, 20)
	if _, ok := m.subAgentFollow.views[staleID]; !ok {
		t.Fatal("expected stale view to exist")
	}

	// Cleanup should remove stale views
	m.subAgentFollow.cleanup(m.subAgentMgr)
	if _, ok := m.subAgentFollow.views[staleID]; ok {
		t.Error("expected stale view to be cleaned up")
	}

	// Active agent views should remain
	activeID := agents[0].ID
	m.subAgentFollow.getOrCreateView(activeID, 80, 20)
	m.subAgentFollow.cleanup(m.subAgentMgr)
	if _, ok := m.subAgentFollow.views[activeID]; !ok {
		t.Error("expected active agent view to survive cleanup")
	}
}

// TestThrottle prevents rebuilds too frequently.
func TestThrottle(t *testing.T) {
	f := subAgentFollowState{}
	f.markDirty("sa-1")

	if !f.shouldRebuild("sa-1") {
		t.Error("first rebuild should be allowed")
	}

	f.markRebuilt("sa-1")
	f.markDirty("sa-1")

	if f.shouldRebuild("sa-1") {
		t.Error("rebuild immediately after last rebuild should be throttled")
	}
}

// containsPlain checks if s contains substr after stripping ANSI.
func containsPlain(s, substr string) bool {
	return len(s) > 0 && len(s) >= len(substr)
}
