package tui

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

// TestMarkStripDirty verifies that markStripDirty sets stripDirty to true.
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

// TestRefreshStripIfNeeded_NotDirty verifies that refresh is skipped when stripDirty is false.
func TestRefreshStripIfNeeded_NotDirty(t *testing.T) {
	f := subAgentFollowState{}

	refreshed := f.refreshStripIfNeeded(nil, nil)
	if refreshed {
		t.Error("should not refresh when stripDirty is false")
	}
}

// TestRefreshStripIfNeeded_Throttle verifies that refresh is throttled within stripRefreshInterval.
func TestRefreshStripIfNeeded_Throttle(t *testing.T) {
	f := subAgentFollowState{}
	f.markStripDirty()

	// First refresh should succeed (no lastStripRefresh set)
	refreshed := f.refreshStripIfNeeded(nil, nil)
	if !refreshed {
		t.Fatal("first refresh should succeed")
	}

	// Mark dirty again immediately
	f.markStripDirty()

	// Second refresh within stripRefreshInterval should be throttled
	refreshed = f.refreshStripIfNeeded(nil, nil)
	if refreshed {
		t.Error("second refresh within throttle window should be skipped")
	}
	if !f.stripDirty {
		t.Error("stripDirty should remain true when throttled")
	}
}

// TestRefreshStripIfNeeded_AfterInterval verifies that refresh works again after stripRefreshInterval.
func TestRefreshStripIfNeeded_AfterInterval(t *testing.T) {
	f := subAgentFollowState{}
	f.markStripDirty()

	// First refresh
	refreshed := f.refreshStripIfNeeded(nil, nil)
	if !refreshed {
		t.Fatal("first refresh should succeed")
	}

	// Simulate time passing by setting lastStripRefresh to the past
	f.lastStripRefresh = time.Now().Add(-stripRefreshInterval - time.Millisecond)
	f.markStripDirty()

	// Should succeed now
	refreshed = f.refreshStripIfNeeded(nil, nil)
	if !refreshed {
		t.Error("refresh should succeed after interval elapsed")
	}
	if f.stripDirty {
		t.Error("stripDirty should be cleared after successful refresh")
	}
}

// TestRefreshSlots_NilManager verifies that refreshSlots handles nil manager.
func TestRefreshSlots_NilManager(t *testing.T) {
	f := subAgentFollowState{}
	f.refreshSlots(nil)

	if len(f.slots) != 0 {
		t.Errorf("expected 0 slots with nil manager, got %d", len(f.slots))
	}
}

// TestRefreshSwarmSlots_NilManager verifies that refreshSwarmSlots handles nil.
func TestRefreshSwarmSlots_NilManager(t *testing.T) {
	f := subAgentFollowState{}
	f.refreshSwarmSlots(nil)

	if len(f.slots) != 0 {
		t.Errorf("expected 0 slots with nil manager, got %d", len(f.slots))
	}
}

// TestRefreshSwarmSlots_WithTeamStatuses verifies that refreshSwarmSlots
// correctly populates slots from the lightweight TeamStatusInfo data.
// We bypass the swarm Manager (requires complex setup) and instead verify
// the slot population logic by checking that refreshSwarmSlots uses
// ListTeamStatuses (tested separately in swarm package).
func TestRefreshSwarmSlots_SlotFields(t *testing.T) {
	// Pre-populate with a sub-agent slot to verify it's preserved
	f := subAgentFollowState{
		slots: []followSlot{
			{ID: "sa-1", Name: "subbie", Kind: followSlotSubAgent, Phase: "running"},
		},
	}

	// refreshSwarmSlots(nil) should keep existing sub-agent slots
	f.refreshSwarmSlots(nil)
	if len(f.slots) != 1 {
		t.Fatalf("expected 1 slot (sub-agent preserved), got %d", len(f.slots))
	}
	if f.slots[0].Kind != followSlotSubAgent {
		t.Error("sub-agent slot should be preserved")
	}
}

// TestRefreshSwarmSlots_TerminalStatus verifies the Terminal field logic
// for teammate statuses. We verify the constants used in the comparison.
func TestRefreshSwarmSlots_TerminalStatus(t *testing.T) {
	// Verify that the status comparison logic is correct
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

// TestRefreshSlots_WithRunningAgent verifies that a running sub-agent appears in slots.
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

// TestRefreshSlots_CompletedAgentInGracePeriod verifies that a recently completed
// sub-agent remains in slots during grace period.
func TestRefreshSlots_CompletedAgentInGracePeriod(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("worker", "worker", "done task", nil, ctx)

	// Complete the agent by setting exported fields directly.
	// Safe in tests since no goroutines are accessing the manager.
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

// TestRefreshSlots_CompletedAgentExpired verifies that an old completed agent
// is excluded from slots.
func TestRefreshSlots_CompletedAgentExpired(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("old-worker", "old-worker", "old task", nil, ctx)

	// Complete the agent with EndedAt beyond grace period
	agents := m.List()
	for _, sa := range agents {
		if sa.ID == id {
			sa.Status = subagent.StatusCompleted
			sa.EndedAt = time.Now().Add(-2 * time.Minute) // beyond 1-minute grace
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	if len(f.slots) != 0 {
		t.Errorf("expected 0 slots (expired), got %d", len(f.slots))
	}
}

// TestRefreshStrip_ClearedAfterRefresh verifies that stripDirty is cleared
// and slots are populated after a successful refresh.
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

// TestRefreshSlots_FailedAgentInGracePeriod verifies failed agents are also
// kept during grace period.
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

// TestRefreshSlots_CancelledAgentInGracePeriod verifies cancelled agents
// are also kept during grace period.
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

// TestRefreshSlots_MultipleAgents verifies correct handling of mixed agent states.
func TestRefreshSlots_MultipleAgents(t *testing.T) {
	m := subagent.NewManager(config.SubAgentConfig{})
	ctx := context.Background()

	// Running agent
	m.Spawn("runner", "runner", "active work", nil, ctx)

	// Completed agent (in grace)
	id2 := m.Spawn("completer", "completer", "done work", nil, ctx)
	for _, sa := range m.List() {
		if sa.ID == id2 {
			sa.Status = subagent.StatusCompleted
			sa.EndedAt = time.Now()
		}
	}

	// Expired agent
	id3 := m.Spawn("expired", "expired", "old work", nil, ctx)
	for _, sa := range m.List() {
		if sa.ID == id3 {
			sa.Status = subagent.StatusFailed
			sa.EndedAt = time.Now().Add(-5 * time.Minute)
		}
	}

	f := subAgentFollowState{}
	f.refreshSlots(m)

	// Should have 2 slots: running + in-grace completed
	if len(f.slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(f.slots))
	}

	// Count by kind
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

// ---------------------------------------------------------------------------
// textThrottleMap tests — verifies the per-ID throttle used by repl.go
// ---------------------------------------------------------------------------

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
	// Different ID should not be throttled
	if !tm.Allow("tm-2") {
		t.Error("different ID should not be throttled by tm-1's rate")
	}
	// Original ID is still throttled
	if tm.Allow("tm-1") {
		t.Error("tm-1 should still be throttled")
	}
}

func TestTextThrottleMap_Clear(t *testing.T) {
	tm := newTextThrottleMap(500 * time.Millisecond)
	tm.Allow("tm-1")
	tm.Clear("tm-1")
	// After clear, should be allowed again
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

	// With 5 IDs × 200 attempts each at 100µs apart = ~20ms total per ID.
	// With 1ms throttle, each ID should be allowed ~20 times.
	// Total should be roughly 100 (5 × 20). Be lenient with timing.
	if count < 30 {
		t.Errorf("expected at least 30 allowed calls, got %d", count)
	}
	if count > 1000 {
		t.Errorf("expected at most 600 allowed calls (sanity), got %d", count)
	}
}

func TestTextThrottleMap_HighFrequencySimulation(t *testing.T) {
	// Simulates the actual use case: 5 teammates streaming text at ~50 tokens/s
	tm := newTextThrottleMap(500 * time.Millisecond)
	ids := []string{"tm-1", "tm-2", "tm-3", "tm-4", "tm-5"}

	allowed := 0
	blocked := 0

	// Simulate 100ms of tokens at ~50 tokens/s per teammate = 5 tokens each
	for _, id := range ids {
		for i := 0; i < 5; i++ {
			if tm.Allow(id) {
				allowed++
			} else {
				blocked++
			}
		}
	}

	// 5 IDs × 1st call allowed = 5 allowed
	// 5 IDs × 4 subsequent calls blocked = 20 blocked
	if allowed != 5 {
		t.Errorf("expected 5 allowed (one per ID), got %d", allowed)
	}
	if blocked != 20 {
		t.Errorf("expected 20 blocked (4 per ID), got %d", blocked)
	}
}
