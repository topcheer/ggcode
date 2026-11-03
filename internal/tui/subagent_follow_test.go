package tui

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

func TestMarkStripDirty(t *testing.T) {
	f := subAgentFollowState{}

	if f.stripDirty {
		t.Error("stripDirty should be false initially")
	}

	f.markStripDirty()
	if !f.stripDirty {
		t.Error("stripDirty should be true after markStripDirty")
	}
}

func TestRefreshStripIfNeeded_NotDirty(t *testing.T) {
	f := subAgentFollowState{}

	refreshed := f.refreshStripIfNeeded(nil, nil)
	if refreshed {
		t.Error("should not refresh when stripDirty is false")
	}
}

func TestRefreshStripIfNeeded_Throttle(t *testing.T) {
	f := subAgentFollowState{}
	f.markStripDirty()

	refreshed := f.refreshStripIfNeeded(nil, nil)
	if !refreshed {
		t.Fatal("first refresh should succeed")
	}

	f.markStripDirty()

	refreshed = f.refreshStripIfNeeded(nil, nil)
	if refreshed {
		t.Error("second refresh within throttle window should be skipped")
	}
	if !f.stripDirty {
		t.Error("stripDirty should remain true when throttled")
	}
}

func TestRefreshStripIfNeeded_AfterInterval(t *testing.T) {
	f := subAgentFollowState{}
	f.markStripDirty()

	refreshed := f.refreshStripIfNeeded(nil, nil)
	if !refreshed {
		t.Fatal("first refresh should succeed")
	}

	f.lastStripRefresh = time.Now().Add(-stripRefreshInterval - time.Millisecond)
	f.markStripDirty()

	refreshed = f.refreshStripIfNeeded(nil, nil)
	if !refreshed {
		t.Error("refresh should succeed after interval elapsed")
	}
	if f.stripDirty {
		t.Error("stripDirty should be cleared after successful refresh")
	}
}

func TestRefreshSlots_NilManager(t *testing.T) {
	f := subAgentFollowState{}
	f.refreshSlots(nil)

	if len(f.slots) != 0 {
		t.Errorf("expected 0 slots with nil manager, got %d", len(f.slots))
	}
}

func TestRefreshSwarmSlots_NilManager(t *testing.T) {
	f := subAgentFollowState{}
	f.refreshSwarmSlots(nil)

	if len(f.slots) != 0 {
		t.Errorf("expected 0 slots with nil manager, got %d", len(f.slots))
	}
}

func TestRefreshSwarmSlots_SlotFields(t *testing.T) {

	f := subAgentFollowState{
		slots: []followSlot{
			{ID: "sa-1", Name: "subbie", Kind: followSlotSubAgent, Phase: "running"},
		},
	}

	f.refreshSwarmSlots(nil)
	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot (sub-agent preserved), got %d", len(f.slots))
	}
	if f.slots[0].Kind != followSlotSubAgent {
		t.Error("sub-agent slot should be preserved")
	}
}

func TestRefreshSwarmSlots_TerminalStatus(t *testing.T) {

	idle := swarm.TeammateIdle
	shutdown := swarm.TeammateShuttingDown
	working := swarm.TeammateWorking

	if idle != "idle" {
		t.Errorf("TeammateIdle should be 'idle', got %s", idle)
	}
	if shutdown != "shutting_down" {
		t.Errorf("TeammateShuttingDown should be 'shutting_down', got %s", shutdown)
	}
	if working != "working" {
		t.Errorf("TeammateWorking should be 'working', got %s", working)
	}
}

func TestRefreshSlots_WithRunningAgent(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("reviewer", "reviewer", "review code", nil, ctx)

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(f.slots))
	}
	if f.slots[0].ID != id {
		t.Errorf("expected slot ID %s, got %s", id, f.slots[0].ID)
	}
	if f.slots[0].Name != "reviewer" {
		t.Errorf("expected name reviewer, got %s", f.slots[0].Name)
	}
	if f.slots[0].Kind != followSlotSubAgent {
		t.Errorf("expected sub-agent kind, got %d", f.slots[0].Kind)
	}
	if f.slots[0].Terminal {
		t.Error("running agent should not be terminal")
	}

	m.CancelAll()
}

func TestRefreshSlots_CompletedAgentInGracePeriod(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("worker", "worker", "done task", nil, ctx)

	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			sa.Status = subagent.StatusCompleted
			sa.EndedAt = time.Now()
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot (grace period), got %d", len(f.slots))
	}
	if f.slots[0].Phase != "done" {
		t.Errorf("expected phase 'done', got %s", f.slots[0].Phase)
	}
	if !f.slots[0].Terminal {
		t.Error("completed agent should be terminal")
	}
}

func TestRefreshSlots_CompletedAgentExpired(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("old-worker", "old-worker", "old task", nil, ctx)

	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			sa.Status = subagent.StatusCompleted
			sa.EndedAt = time.Now().Add(-2 * time.Minute)
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 0 {
		t.Errorf("expected 0 slots (expired), got %d", len(f.slots))
	}
}

func TestBuildFollowListUnknownToolUsesCommandDetail(t *testing.T) {
	list := chat.NewList(100, 20)
	buildFollowList(followEventData{
		ID:     "sa-1",
		Name:   "OpenCode",
		Task:   "Review current changes",
		Status: "running",
		Events: []followEvent{
			{Type: followEventToolCall, ToolID: "tool-1", ToolName: "Run", ToolArgs: `{"command":"git diff --cached"}`},
		},
	}, list, chat.DefaultStyles())

	if list.Len() != 2 {
		t.Fatalf("expected header + tool item, got %d items", list.Len())
	}
	item := list.ItemAt(1)
	paramsItem, ok := item.(interface{ RenderParams() string })
	if !ok {
		t.Fatalf("expected tool item with params, got %T", item)
	}
	if got := paramsItem.RenderParams(); got != "git diff --cached" {
		t.Fatalf("expected command detail in follow item, got %q", got)
	}
}

func TestRefreshStrip_ClearedAfterRefresh(t *testing.T) {
	saMgr := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	saMgr.Spawn("agent-1", "agent-1", "work", nil, ctx)

	f := subAgentFollowState{}
	f.markStripDirty()

	refreshed := f.refreshStripIfNeeded(saMgr, nil)
	if !refreshed {
		t.Fatal("should refresh when dirty")
	}
	if f.stripDirty {
		t.Error("stripDirty should be cleared after refresh")
	}
	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(f.slots))
	}

	saMgr.CancelAll()
}

func TestRefreshSlots_FailedAgentInGracePeriod(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("failer", "failer", "failing task", nil, ctx)

	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			sa.Status = subagent.StatusFailed
			sa.EndedAt = time.Now()
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot (failed, grace period), got %d", len(f.slots))
	}
	if f.slots[0].Phase != "done" {
		t.Errorf("expected phase 'done', got %s", f.slots[0].Phase)
	}
	if !f.slots[0].Terminal {
		t.Error("failed agent should be terminal")
	}
}

func TestRefreshSlots_CancelledAgentInGracePeriod(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("canceller", "canceller", "cancelled task", nil, ctx)

	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			sa.Status = subagent.StatusCancelled
			sa.EndedAt = time.Now()
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot (cancelled, grace period), got %d", len(f.slots))
	}
}

func TestRefreshSlots_MultipleAgents(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()

	m.Spawn("runner", "runner", "active work", nil, ctx)

	id2 := m.Spawn("completer", "completer", "done work", nil, ctx)
	for _, sa := range m.List() {
		if sa.ID == id2 {
			sa.Status = subagent.StatusCompleted
			sa.EndedAt = time.Now()
		}
	}

	id3 := m.Spawn("expired", "expired", "old work", nil, ctx)
	for _, sa := range m.List() {
		if sa.ID == id3 {
			sa.Status = subagent.StatusFailed
			sa.EndedAt = time.Now().Add(-5 * time.Minute)
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(f.slots))
	}

	terminal := 0
	nonTerminal := 0
	for _, s := range f.slots {
		if s.Terminal {
			terminal++
		} else {
			nonTerminal++
		}
	}
	if nonTerminal != 1 {
		t.Errorf("expected 1 non-terminal (running), got %d", nonTerminal)
	}
	if terminal != 1 {
		t.Errorf("expected 1 terminal (completed in grace), got %d", terminal)
	}
}

func TestTextThrottleMap_FirstAllow(t *testing.T) {
	tm := newTextThrottleMap(500 * time.Millisecond)
	if !tm.Allow("tm-1") {
		t.Error("first Allow should return true")
	}
}

func TestTextThrottleMap_Throttled(t *testing.T) {
	tm := newTextThrottleMap(500 * time.Millisecond)
	tm.Allow("tm-1")
	if tm.Allow("tm-1") {
		t.Error("second Allow within delay should return false")
	}
}

func TestTextThrottleMap_AfterDelay(t *testing.T) {
	tm := newTextThrottleMap(1 * time.Millisecond)
	tm.Allow("tm-1")
	time.Sleep(2 * time.Millisecond)
	if !tm.Allow("tm-1") {
		t.Error("Allow after delay elapsed should return true")
	}
}

func TestTextThrottleMap_PerID(t *testing.T) {
	tm := newTextThrottleMap(500 * time.Millisecond)
	tm.Allow("tm-1")

	if !tm.Allow("tm-2") {
		t.Error("different ID should not be throttled by tm-1's rate")
	}

	if tm.Allow("tm-1") {
		t.Error("tm-1 should still be throttled")
	}
}

func TestTextThrottleMap_Clear(t *testing.T) {
	tm := newTextThrottleMap(500 * time.Millisecond)
	tm.Allow("tm-1")
	tm.Clear("tm-1")

	if !tm.Allow("tm-1") {
		t.Error("Allow after Clear should return true")
	}
}

func TestTextThrottleMap_Concurrent(t *testing.T) {
	tm := newTextThrottleMap(1 * time.Millisecond)
	var wg sync.WaitGroup
	allowed := make(chan string, 2000)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if tm.Allow(id) {
					allowed <- id
				}
				time.Sleep(100 * time.Microsecond)
			}
		}(fmt.Sprintf("tm-%d", i))
	}

	go func() {
		wg.Wait()
		close(allowed)
	}()

	count := 0
	for range allowed {
		count++
	}

	if count < 30 {
		t.Errorf("expected at least 30 allowed calls, got %d", count)
	}
	if count > 1000 {
		t.Errorf("expected at most 600 allowed calls (sanity), got %d", count)
	}
}

func TestTextThrottleMap_HighFrequencySimulation(t *testing.T) {

	tm := newTextThrottleMap(500 * time.Millisecond)
	ids := []string{"tm-1", "tm-2", "tm-3", "tm-4", "tm-5"}

	allowed := 0
	blocked := 0

	for _, id := range ids {
		for i := 0; i < 5; i++ {
			if tm.Allow(id) {
				allowed++
			} else {
				blocked++
			}
		}
	}

	if allowed != 5 {
		t.Errorf("expected 5 allowed (one per ID), got %d", allowed)
	}
	if blocked != 20 {
		t.Errorf("expected 20 blocked (4 per ID), got %d", blocked)
	}
}
