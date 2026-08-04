package agent

import (
	"strings"
	"testing"
	"time"
)

func TestDelegationState_OrphanDetection(t *testing.T) {
	d := newDelegationState()

	// Simulate a delegation at iteration 2
	d.recordDelegationCall("agent-1", "spawn_agent", "Research code patterns", 2)
	d.recordToolCallCount()

	// Immediately after, no orphan
	if msg := d.maybeWarnOrphanedDelegations(3); msg != "" {
		t.Fatalf("expected no orphan warning at iteration 3, got: %s", msg)
	}

	// After threshold iterations without a result check, should warn
	msg := d.maybeWarnOrphanedDelegations(2 + orphanDelegationThreshold + 1)
	if msg == "" {
		t.Fatal("expected orphan warning after threshold iterations")
	}
	if !strings.Contains(msg, "spawn_agent") {
		t.Errorf("orphan warning should mention the delegation tool, got: %s", msg)
	}
	if !strings.Contains(msg, "Research code patterns") {
		t.Errorf("orphan warning should include task summary, got: %s", msg)
	}

	// Should not warn again for the same delegation
	msg2 := d.maybeWarnOrphanedDelegations(2 + orphanDelegationThreshold + 5)
	if msg2 != "" {
		t.Errorf("should not re-warn for same delegation, got: %s", msg2)
	}
}

func TestDelegationState_ResultCheckClearsOrphan(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("agent-1", "spawn_agent", "Analyze tests", 1)
	d.recordToolCallCount()

	// Check result at iteration 5, which updates lastChecked to 5
	d.recordResultCheck("wait_agent", 5)

	// Check at iteration 6: elapsed = 6-5 = 1, below threshold
	msg := d.maybeWarnOrphanedDelegations(6)
	if msg != "" {
		t.Fatalf("expected no orphan after result check, got: %s", msg)
	}
}

func TestDelegationState_OrphanMaxWarnings(t *testing.T) {
	d := newDelegationState()

	// Create many orphaned delegations
	for i := 0; i < orphanDelegationMaxWarnings+3; i++ {
		d.recordDelegationCall("agent-"+itoaDel(i), "spawn_agent", "Task "+itoaDel(i), 1)
	}

	// Each call should only warn up to the max
	warnCount := 0
	for iter := 5; iter < 20; iter++ {
		msg := d.maybeWarnOrphanedDelegations(iter)
		if msg != "" {
			warnCount++
		}
	}

	if warnCount > orphanDelegationMaxWarnings {
		t.Errorf("orphan warnings (%d) exceeded max (%d)", warnCount, orphanDelegationMaxWarnings)
	}
}

func TestDelegationState_OrphanTimeBased(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("agent-1", "spawn_agent", "Old task", 1)

	// Even with low iteration count, old delegations should be caught
	// We can't easily fast-forward time, but we verify the entry has a creation time
	d.mu.Lock()
	entry := d.activeDelegations["agent-1"]
	d.mu.Unlock()

	if entry == nil {
		t.Fatal("delegation not tracked")
	}
	if time.Since(entry.creationTime) > time.Second {
		t.Error("creation time should be very recent")
	}
}

func TestDelegationState_SerialDelegationDetection(t *testing.T) {
	d := newDelegationState()

	// Two consecutive delegations - should not warn yet
	d.recordDelegationCall("a1", "spawn_agent", "Task 1", 1)
	msg := d.maybeWarnSerialDelegation()
	if msg != "" {
		t.Fatalf("expected no serial warning for 1 delegation, got: %s", msg)
	}

	d.recordDelegationCall("a2", "spawn_agent", "Task 2", 2)
	msg = d.maybeWarnSerialDelegation()
	if msg != "" {
		t.Fatalf("expected no serial warning for 2 delegations, got: %s", msg)
	}

	// Third consecutive delegation - should warn
	d.recordDelegationCall("a3", "spawn_agent", "Task 3", 3)
	msg = d.maybeWarnSerialDelegation()
	if msg == "" {
		t.Fatal("expected serial delegation warning for 3 consecutive")
	}
	if !strings.Contains(msg, "batch") || !strings.Contains(msg, "parallel") {
		t.Errorf("serial warning should recommend batching/parallelization, got: %s", msg)
	}

	// Non-delegation tool breaks the chain
	d.recordToolCallCount()
	msg = d.maybeWarnSerialDelegation()
	// After non-delegation call, the consecutive counter resets
}

func TestDelegationState_SerialDelegationMaxWarnings(t *testing.T) {
	d := newDelegationState()

	// Trigger serial warnings
	for i := 0; i < serialDelegationThreshold; i++ {
		d.recordDelegationCall("s"+itoaDel(i), "spawn_agent", "Serial "+itoaDel(i), i+1)
	}

	msg1 := d.maybeWarnSerialDelegation()
	if msg1 == "" {
		t.Fatal("expected first serial warning")
	}

	// Add more and check again
	for i := 0; i < serialDelegationThreshold; i++ {
		d.recordDelegationCall("t"+itoaDel(i), "spawn_agent", "Serial2 "+itoaDel(i), i+10)
	}
	_ = d.maybeWarnSerialDelegation()
	// Should still respect max warnings

	// Third attempt should be suppressed
	for i := 0; i < serialDelegationThreshold; i++ {
		d.recordDelegationCall("u"+itoaDel(i), "spawn_agent", "Serial3 "+itoaDel(i), i+20)
	}
	msg3 := d.maybeWarnSerialDelegation()
	if msg3 != "" {
		t.Errorf("third serial warning should be suppressed (max=%d), got: %s", serialDelegationMaxWarnings, msg3)
	}
}

func TestDelegationState_OverDelegationDetection(t *testing.T) {
	d := newDelegationState()

	// Fill with mostly delegation calls
	for i := 0; i < overDelegationMinCalls+5; i++ {
		d.recordDelegationCall("d"+itoaDel(i), "spawn_agent", "Delegate "+itoaDel(i), i)
		d.recordToolCallCount()
	}

	msg := d.maybeWarnOverDelegation()
	if msg == "" {
		t.Fatal("expected over-delegation warning")
	}
	if !strings.Contains(msg, "High delegation ratio") {
		t.Errorf("over-delegation warning should mention ratio, got: %s", msg)
	}

	// Should only warn once
	msg2 := d.maybeWarnOverDelegation()
	if msg2 != "" {
		t.Error("over-delegation should only warn once per run")
	}
}

func TestDelegationState_NoOverDelegationForLowRatio(t *testing.T) {
	d := newDelegationState()

	// Mostly direct tool calls with few delegations
	d.recordDelegationCall("d1", "spawn_agent", "One task", 1)
	for i := 0; i < 19; i++ {
		d.recordToolCallCount()
	}

	msg := d.maybeWarnOverDelegation()
	if msg != "" {
		t.Errorf("should not warn for low delegation ratio (1/20), got: %s", msg)
	}
}

func TestDelegationState_NoOverDelegationBelowMinCalls(t *testing.T) {
	d := newDelegationState()

	// All delegation calls but below minimum threshold
	for i := 0; i < 8; i++ {
		d.recordDelegationCall("d"+itoaDel(i), "spawn_agent", "Task "+itoaDel(i), i)
		d.recordToolCallCount()
	}

	msg := d.maybeWarnOverDelegation()
	if msg != "" {
		t.Errorf("should not warn below min calls (%d), got: %s", overDelegationMinCalls, msg)
	}
}

func TestDelegationState_ResetForNewTurn(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("a1", "spawn_agent", "Task", 1)
	d.recordToolCallCount()
	d.recordToolCallCount()

	d.resetForNewTurn()

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.activeDelegations) != 0 {
		t.Errorf("activeDelegations not cleared after reset: %d", len(d.activeDelegations))
	}
	if d.totalDelegations != 0 {
		t.Errorf("totalDelegations not reset: %d", d.totalDelegations)
	}
	if d.totalToolCalls != 0 {
		t.Errorf("totalToolCalls not reset: %d", d.totalToolCalls)
	}
	if len(d.delegationHistory) != 0 {
		t.Errorf("delegationHistory not cleared: %d", len(d.delegationHistory))
	}
}

func TestDelegationState_RemoveDelegation(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("agent-42", "spawn_agent", "To be removed", 1)
	if len(d.activeDelegations) != 1 {
		t.Fatal("delegation not tracked")
	}

	d.removeDelegation("agent-42")
	if len(d.activeDelegations) != 0 {
		t.Error("delegation not removed")
	}
}

func TestDelegationState_NonDelegationToolNotTracked(t *testing.T) {
	d := newDelegationState()

	// recordToolCallCount doesn't add to delegation tracking
	d.recordToolCallCount()
	d.recordToolCallCount()

	msg := d.maybeWarnOrphanedDelegations(10)
	if msg != "" {
		t.Errorf("non-delegation tool calls should not trigger orphan warnings: %s", msg)
	}

	msg = d.maybeWarnSerialDelegation()
	if msg != "" {
		t.Errorf("non-delegation tool calls should not trigger serial warnings: %s", msg)
	}
}

func TestDelegationState_DifferentDelegationToolsTracked(t *testing.T) {
	tools := []string{"spawn_agent", "delegate", "teammate_spawn", "swarm_task_create",
		"use_namedagent", "a2a_remote", "a2a_send_task"}

	for _, tool := range tools {
		d := newDelegationState()
		d.recordDelegationCall("id-"+tool, tool, "Task with "+tool, 1)
		if len(d.activeDelegations) != 1 {
			t.Errorf("tool %s not tracked as delegation", tool)
		}
	}
}

func TestDelegationState_ResultToolsClearOrphan(t *testing.T) {
	resultTools := []string{"wait_agent", "list_agents", "task_output",
		"teammate_results", "swarm_task_list", "a2a_get_task", "a2a_list_tasks"}

	for _, tool := range resultTools {
		d := newDelegationState()
		d.recordDelegationCall("agent-1", "spawn_agent", "Task", 1)
		// Result check at iteration 5 updates lastChecked to 5
		d.recordResultCheck(tool, 5)
		// Check at iteration 6: elapsed = 6-5 = 1, which is below threshold
		msg := d.maybeWarnOrphanedDelegations(6)
		if msg != "" {
			t.Errorf("result tool %s did not clear orphan timer: %s", tool, msg)
		}
	}
}

func TestTruncateDelSummary(t *testing.T) {
	short := "Do something"
	if got := truncateDelSummary(short); got != short {
		t.Errorf("short summary changed: got %q", got)
	}

	long := strings.Repeat("x", 100)
	got := truncateDelSummary(long)
	if len(got) != 80 {
		t.Errorf("truncated summary should be 80 chars, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("truncated summary should end with ...")
	}
}

func TestItoaDel(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{100, "100"},
		{-5, "-5"},
		{42, "42"},
	}

	for _, tt := range tests {
		if got := itoaDel(tt.input); got != tt.want {
			t.Errorf("itoaDel(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
