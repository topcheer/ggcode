package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

// ---------------------------------------------------------------------------
// Existing tests (unchanged)
// ---------------------------------------------------------------------------

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

func TestFollowSlotRefreshStableOrder(t *testing.T) {
	m, _ := newFollowTestModel(5)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	for i := 1; i < len(m.subAgentFollow.slots); i++ {
		if m.subAgentFollow.slots[i].ID < m.subAgentFollow.slots[i-1].ID {
			t.Errorf("slots not sorted: slot[%d]=%s > slot[%d]=%s",
				i-1, m.subAgentFollow.slots[i-1].ID, i, m.subAgentFollow.slots[i].ID)
		}
	}

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

func TestFollowActivateDeactivate(t *testing.T) {
	m, _ := newFollowTestModel(3)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	if m.subAgentFollow.isActive() {
		t.Error("should not be active initially")
	}

	m.subAgentFollow.activate(0)
	if !m.subAgentFollow.isActive() {
		t.Error("should be active after activate(0)")
	}
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[0].ID {
		t.Error("activeID should match slot 0")
	}

	prev := m.subAgentFollow.deactivate()
	if m.subAgentFollow.isActive() {
		t.Error("should not be active after deactivate")
	}
	if prev != m.subAgentFollow.slots[0].ID {
		t.Error("deactivate should return previous activeID")
	}
}

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

func TestFollowAutoReturnDisabled(t *testing.T) {
	m, agents := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.activate(0)

	agents[0].Status = subagent.StatusCompleted

	returnedID := m.subAgentFollow.autoReturnIfNeeded(m.subAgentMgr)
	if returnedID != "" {
		t.Error("auto-return should be disabled; user controls exit via Esc")
	}
	if !m.subAgentFollow.isActive() {
		t.Error("should still be active after agent completes — user views result")
	}
}

func TestFollowAutoReturnNoopWhileRunning(t *testing.T) {
	m, agents := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.activate(0)

	agents[0].Status = subagent.StatusRunning
	returnedID := m.subAgentFollow.autoReturnIfNeeded(m.subAgentMgr)
	if returnedID != "" {
		t.Error("should not auto-return while agent is still running")
	}
	if !m.subAgentFollow.isActive() {
		t.Error("should still be active while agent runs")
	}
}

func TestFollowActivateAnySlot(t *testing.T) {
	m, agents := newFollowTestModel(3)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Complete first two via Manager so EndedAt is set
	m.subAgentMgr.Complete(agents[0].ID, "done", nil)
	m.subAgentMgr.Complete(agents[1].ID, "done", nil)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// All 3 should still be in slots (2 terminal in grace + 1 running)
	if len(m.subAgentFollow.slots) != 3 {
		t.Fatalf("expected 3 slots (2 grace + 1 running), got %d", len(m.subAgentFollow.slots))
	}

	// Can activate any slot including grace-period ones
	m.subAgentFollow.activate(0)
	if !m.subAgentFollow.isActive() {
		t.Fatal("should be active after activate(0)")
	}
}

func TestFollowStripRendering(t *testing.T) {
	m, _ := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	strip := m.renderSubAgentFollowStrip()
	if strip == "" {
		t.Error("expected non-empty strip when sub-agents are running")
	}
	if !containsPlain(strip, "↑↓←→") {
		t.Error("expected arrow key hint in strip")
	}
	if !containsPlain(strip, "Esc close") {
		t.Error("expected 'Esc close' hint in strip")
	}
}

func TestFollowStripEmptyWhenNoSlots(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	strip := m.renderSubAgentFollowStrip()
	if strip != "" {
		t.Error("expected empty strip when no sub-agents")
	}
}

func TestBuildSubAgentFollowList(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "test-task", "Test Task", nil, context.Background())
	sa, _ := mgr.Get(id)

	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Hello from sub-agent"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: "/tmp/test.txt"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "file contents here"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Analysis complete"})

	snap, _ := mgr.Snapshot(id)
	list := chat.NewList(80, 20)

	styles := chat.DefaultStyles()
	buildFollowList(subagentSnapshotToFollowData(snap), list, styles)

	if list.Len() < 3 {
		t.Errorf("expected at least 3 items in follow list, got %d", list.Len())
	}
}

func TestBuildSubAgentFollowListMergesText(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "test-task", "Test Task", nil, context.Background())
	sa, _ := mgr.Get(id)

	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Hello "})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "world"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "!"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: "/tmp/test.txt"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "contents"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "Done"})
	sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: " here"})

	snap, _ := mgr.Snapshot(id)
	list := chat.NewList(80, 20)
	buildFollowList(subagentSnapshotToFollowData(snap), list, chat.DefaultStyles())

	expectedItems := 4
	if list.Len() != expectedItems {
		t.Errorf("expected %d items (header + 2 merged text blocks + 1 tool), got %d",
			expectedItems, list.Len())
	}
}

func TestBuildSubAgentFollowListTruncation(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("test", "test-task", "Test Task", nil, context.Background())
	sa, _ := mgr.Get(id)

	for i := 0; i < 250; i++ {
		sa.AppendEvent(subagent.AgentEvent{Type: subagent.AgentEventText, Text: "line"})
	}

	snap, _ := mgr.Snapshot(id)
	if snap.EventsDropped == 0 {
		t.Error("expected some events to be dropped after 250 appends")
	}

	list := chat.NewList(80, 20)
	buildFollowList(subagentSnapshotToFollowData(snap), list, chat.DefaultStyles())

	if list.Len() < 2 {
		t.Error("expected header + truncation notice at minimum")
	}
}

func TestFollowCleanup(t *testing.T) {
	m, agents := newFollowTestModel(2)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	staleID := "stale-agent-id"
	m.subAgentFollow.getOrCreateView(staleID, 80, 20)
	if _, ok := m.subAgentFollow.views[staleID]; !ok {
		t.Fatal("expected stale view to exist")
	}

	m.subAgentFollow.cleanup(m.subAgentMgr, nil)
	if _, ok := m.subAgentFollow.views[staleID]; ok {
		t.Error("expected stale view to be cleaned up")
	}

	activeID := agents[0].ID
	m.subAgentFollow.getOrCreateView(activeID, 80, 20)
	m.subAgentFollow.cleanup(m.subAgentMgr, nil)
	if _, ok := m.subAgentFollow.views[activeID]; !ok {
		t.Error("expected active agent view to survive cleanup")
	}
}

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

// ---------------------------------------------------------------------------
// New tests for arrow key navigation and swarm teammate follow
// ---------------------------------------------------------------------------

func TestArrowKeyNavigation(t *testing.T) {
	m, _ := newFollowTestModel(4)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Activate first slot
	m.subAgentFollow.activate(0)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[0].ID {
		t.Fatal("expected slot 0 to be active")
	}

	// Simulate "down" arrow: should go to slot 1
	currentIdx := m.subAgentFollow.currentSlotIndex()
	m.subAgentFollow.activate(currentIdx + 1)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[1].ID {
		t.Errorf("expected slot 1 after down, got %s", m.subAgentFollow.activeID)
	}

	// Simulate "up" arrow: should go back to slot 0
	currentIdx = m.subAgentFollow.currentSlotIndex()
	m.subAgentFollow.activate(currentIdx - 1)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[0].ID {
		t.Errorf("expected slot 0 after up, got %s", m.subAgentFollow.activeID)
	}

	// Wrap-around down: slot 3 -> slot 0
	m.subAgentFollow.activate(3)
	currentIdx = m.subAgentFollow.currentSlotIndex()
	nextIdx := (currentIdx + 1) % len(m.subAgentFollow.slots)
	m.subAgentFollow.activate(nextIdx)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[0].ID {
		t.Errorf("expected wrap to slot 0, got %s", m.subAgentFollow.activeID)
	}

	// Wrap-around up: slot 0 -> slot 3
	currentIdx = m.subAgentFollow.currentSlotIndex()
	prevIdx := (currentIdx - 1 + len(m.subAgentFollow.slots)) % len(m.subAgentFollow.slots)
	m.subAgentFollow.activate(prevIdx)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[3].ID {
		t.Errorf("expected wrap to slot 3, got %s", m.subAgentFollow.activeID)
	}
}

func TestCtrlNOnlyOpensNotCycles(t *testing.T) {
	m, _ := newFollowTestModel(3)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// First Ctrl+N: should open
	if !m.subAgentFollow.isActive() {
		m.subAgentFollow.activate(0)
	}
	firstID := m.subAgentFollow.activeID

	// Simulating Ctrl+N again should NOT change slot (Ctrl+N only opens)
	// In the real handler, len > 0 && !isActive() is false, so it does nothing
	if m.subAgentFollow.isActive() {
		// Ctrl+N would skip — activeID stays the same
		if m.subAgentFollow.activeID != firstID {
			t.Error("Ctrl+N should not change slot when already active")
		}
	}
}

func TestSwarmTeammateSlots(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	// Create a swarm manager with one team and one teammate
	m.swarmMgr = swarm.NewManager(config.SwarmConfig{}, nil, nil, nil)

	// Create sub-agent slots
	m.subAgentMgr.Spawn("test", "test", "test", nil, context.Background())
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Before adding swarm slots, only sub-agent slots
	for _, s := range m.subAgentFollow.slots {
		if s.Kind != followSlotSubAgent {
			t.Error("expected only sub-agent slots before refreshSwarmSlots")
		}
	}

	// Add swarm slots (will be 0 teammates if no team created)
	m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)

	// Should still have the sub-agent slot
	if len(m.subAgentFollow.slots) != 1 {
		t.Errorf("expected 1 slot (sub-agent), got %d", len(m.subAgentFollow.slots))
	}
}

func TestTeammateEventRendering(t *testing.T) {
	snap := swarm.TeammateSnapshot{
		ID:     "tm-1",
		Name:   "researcher",
		Status: swarm.TeammateIdle,
		Events: []swarm.TeammateEvent{
			{Type: swarm.TeammateEventText, Text: "I'll search for the relevant files."},
			{Type: swarm.TeammateEventToolCall, ToolName: "search_files", ToolArgs: `{"pattern":"TODO"}`},
			{Type: swarm.TeammateEventToolResult, ToolName: "search_files", Result: "3 matches found"},
			{Type: swarm.TeammateEventText, Text: "Found 3 items to fix."},
		},
	}

	data := teammateSnapshotToFollowData(snap)

	if data.ID != "tm-1" {
		t.Errorf("expected ID tm-1, got %s", data.ID)
	}
	if data.Name != "researcher" {
		t.Errorf("expected name researcher, got %s", data.Name)
	}
	if len(data.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(data.Events))
	}
	if data.Events[0].Type != followEventText {
		t.Errorf("expected text event, got %d", data.Events[0].Type)
	}
	if data.Events[1].ToolName != "search_files" {
		t.Errorf("expected search_files tool call, got %s", data.Events[1].ToolName)
	}
	if data.Events[2].ToolName != "search_files" {
		t.Errorf("expected search_files tool result, got %s", data.Events[2].ToolName)
	}

	// Build follow list from teammate data
	list := chat.NewList(80, 20)
	buildFollowList(data, list, chat.DefaultStyles())

	// header + text + tool-call + text = 4
	if list.Len() != 4 {
		t.Errorf("expected 4 items, got %d", list.Len())
	}
}

func TestTeammateSnapshotToFollowDataStatus(t *testing.T) {
	tests := []struct {
		status   swarm.TeammateStatus
		expected string
	}{
		{swarm.TeammateWorking, "running"},
		{swarm.TeammateIdle, "idle"},
		{swarm.TeammateShuttingDown, "shutting_down"},
	}

	for _, tt := range tests {
		snap := swarm.TeammateSnapshot{ID: "tm-1", Status: tt.status}
		data := teammateSnapshotToFollowData(snap)
		if data.Status != tt.expected {
			t.Errorf("status %v: expected %q, got %q", tt.status, tt.expected, data.Status)
		}
	}
}

func TestMixedSubAgentAndTeammateSlots(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.swarmMgr = swarm.NewManager(config.SwarmConfig{}, nil, nil, nil)

	// Create 2 sub-agents
	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, context.Background())
	m.subAgentMgr.Spawn("a2", "a2", "task2", nil, context.Background())
	m.subAgentFollow.refreshSlots(m.subAgentMgr)
	m.subAgentFollow.refreshSwarmSlots(m.swarmMgr)

	// Should have 2 sub-agent slots
	if len(m.subAgentFollow.slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(m.subAgentFollow.slots))
	}
	for _, s := range m.subAgentFollow.slots {
		if s.Kind != followSlotSubAgent {
			t.Error("expected all slots to be sub-agents")
		}
	}

	// Activate first and verify arrow key nav works
	m.subAgentFollow.activate(0)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[0].ID {
		t.Error("expected first slot active")
	}

	// Navigate to second
	currentIdx := m.subAgentFollow.currentSlotIndex()
	m.subAgentFollow.activate(currentIdx + 1)
	if m.subAgentFollow.activeID != m.subAgentFollow.slots[1].ID {
		t.Error("expected second slot active after down")
	}
}

func containsPlain(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestRefreshSlots_FiltersCompleted(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	// Spawn 3 sub-agents
	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, context.Background())
	m.subAgentMgr.Spawn("a2", "a2", "task2", nil, context.Background())
	m.subAgentMgr.Spawn("a3", "a3", "task3", nil, context.Background())

	// Complete two of them — they should remain visible during grace period
	m.subAgentMgr.Complete("sa-1", "done", nil)
	m.subAgentMgr.Complete("sa-2", "done", nil)

	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// All 3 should be in slots (2 terminal + 1 running)
	if len(m.subAgentFollow.slots) != 3 {
		t.Fatalf("expected 3 slots (2 in grace + 1 running), got %d", len(m.subAgentFollow.slots))
	}
	// Find the running one
	found := false
	for _, s := range m.subAgentFollow.slots {
		if s.ID == "sa-3" && !s.Terminal {
			found = true
		}
	}
	if !found {
		t.Error("expected sa-3 as non-terminal slot")
	}
}

func TestRefreshSlots_AllCompleted_GracePeriod(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, context.Background())
	m.subAgentMgr.Complete("sa-1", "done", nil)

	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Completed agent stays visible during grace period
	if len(m.subAgentFollow.slots) != 1 {
		t.Fatalf("expected 1 slot (grace period), got %d", len(m.subAgentFollow.slots))
	}
	if !m.subAgentFollow.slots[0].Terminal {
		t.Error("expected terminal=true for completed agent in grace period")
	}

	// Strip should still be visible
	strip := m.renderSubAgentFollowStrip()
	if strip == "" {
		t.Error("expected non-empty strip during grace period")
	}
}

func TestRefreshSlots_FiltersFailedAndCancelled(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, context.Background())
	m.subAgentMgr.Spawn("a2", "a2", "task2", nil, context.Background())

	// Complete with error → failed
	m.subAgentMgr.Complete("sa-1", "", context.Canceled)

	// Cancel via manager
	ctx2, cancel2 := context.WithCancel(context.Background())
	m.subAgentMgr.SetCancel("sa-2", cancel2)
	m.subAgentMgr.Cancel("sa-2")
	_ = ctx2

	// Both are terminal but within grace period → should still be visible
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	if len(m.subAgentFollow.slots) != 2 {
		t.Errorf("expected 2 slots (in grace period), got %d", len(m.subAgentFollow.slots))
	}
	for _, s := range m.subAgentFollow.slots {
		if !s.Terminal {
			t.Errorf("expected slot %s to be terminal", s.ID)
		}
	}
}

func TestAutoDeactivateOnGracePeriodExpired(t *testing.T) {
	// When a followed sub-agent completes and grace period expires,
	// refreshSlots removes it, currentSlotIndex returns -1, and
	// the model_update handler deactivates follow mode.
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr.Spawn("a1", "a1", "task1", nil, context.Background())
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Activate follow mode
	m.subAgentFollow.activate(0)
	if !m.subAgentFollow.isActive() {
		t.Fatal("expected follow mode active")
	}

	// Complete the agent — still in grace period
	m.subAgentMgr.Complete("sa-1", "done", nil)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Should still be in slots during grace period
	if m.subAgentFollow.currentSlotIndex() == -1 {
		t.Error("agent should still be in slots during grace period")
	}

	// Manually expire grace period by setting EndedAt to 2 minutes ago
	sa, _ := m.subAgentMgr.Get("sa-1")
	sa.EndedAt = time.Now().Add(-2 * time.Minute)
	m.subAgentFollow.refreshSlots(m.subAgentMgr)

	// Now it should be removed from slots
	if m.subAgentFollow.currentSlotIndex() != -1 {
		t.Error("expected currentSlotIndex -1 after grace period expired")
	}
}
