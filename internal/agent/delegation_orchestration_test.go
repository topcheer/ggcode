package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDelegationState_OrphanDetection(t *testing.T) {
	d := newDelegationState()

	// Simulate a delegation at iteration 2
	d.recordDelegationCall("agent-1", "spawn_agent", "Research code patterns", "", 2)
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

	// tool-call ID and returned agent ID are DIFFERENT namespaces (#348):
	// spawn result says "Sub-agent spawned with ID: sa-9" but the provider
	// tool-call ID is toolu_01ABC. wait_agent addresses sa-9.
	d.recordDelegationCall("toolu_01ABC", "spawn_agent", "Analyze tests",
		"Sub-agent spawned with ID: sa-9\nUse wait_agent or list_agents to monitor progress and retrieve the result.", 1)
	d.recordToolCallCount()

	// Targeted result check addressing sa-9 at iteration 5 updates its lastChecked
	d.recordResultCheck("wait_agent", json.RawMessage(`{"agent_id":"sa-9"}`), "", 5)

	// Check at iteration 6: elapsed = 6-5 = 1, below threshold
	msg := d.maybeWarnOrphanedDelegations(6)
	if msg != "" {
		t.Fatalf("expected no orphan after result check, got: %s", msg)
	}

	// Production regression (#348): with a fixed 1s-timer-style mismatch the
	// wait could never clear the timer. Verify after threshold iterations the
	// consumed delegation still does not warn.
	if msg := d.maybeWarnOrphanedDelegations(5 + orphanDelegationThreshold - 1); msg != "" {
		t.Fatalf("consumed delegation must stay cleared even near threshold, got: %s", msg)
	}
}

func TestDelegationState_ResultCheckOnlyMarksAddressedDelegation(t *testing.T) {
	d := newDelegationState()

	// Distinct namespaces per #348: tool-call IDs toolu_01/toolu_02, agent IDs sa-1/sa-2.
	d.recordDelegationCall("toolu_01", "spawn_agent", "Task one",
		"Sub-agent spawned with ID: sa-1", 1)
	d.recordDelegationCall("toolu_02", "spawn_agent", "Task two",
		"Sub-agent spawned with ID: sa-2", 1)

	// Wait on sa-1 only — sa-2's orphan timer must NOT be reset.
	d.recordResultCheck("task_output", json.RawMessage(`{"task_id":"sa-1"}`), "", 5)

	// sa-2 spawned at 1, last checked 1; at iteration 1+orphanDelegationThreshold
	// it is an orphan even though sa-1 was just consumed.
	msg := d.maybeWarnOrphanedDelegations(1 + orphanDelegationThreshold)
	if msg == "" {
		t.Fatal("expected orphan warning for un-addressed delegation")
	}
	if !strings.Contains(msg, "Task two") {
		t.Errorf("orphan warning should name the unchecked delegation, got: %s", msg)
	}
	if strings.Contains(msg, "Task one") {
		t.Errorf("orphan warning must not include the checked delegation, got: %s", msg)
	}
}

func TestDelegationState_UnresolvableResultCheckMarksNothing(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("agent-1", "spawn_agent", "Task", "", 1)

	// wait_agent with no resolvable target and no id-bearing content —
	// conservative: mark nothing.
	d.recordResultCheck("wait_agent", nil, "", 5)

	d.mu.Lock()
	last := d.activeDelegations["agent-1"].lastChecked
	d.mu.Unlock()
	if last != 1 {
		t.Errorf("unresolvable result check should not mark delegation, lastChecked=%d want 1", last)
	}
}

func TestDelegationState_OrphanMaxWarnings(t *testing.T) {
	d := newDelegationState()

	// Create many orphaned delegations
	for i := 0; i < orphanDelegationMaxWarnings+3; i++ {
		d.recordDelegationCall("agent-"+itoaDel(i), "spawn_agent", "Task "+itoaDel(i), "", 1)
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

func TestDelegationState_OrphanTimeFallbackRemoved(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("agent-1", "spawn_agent", "Old task", "", 1)

	// Entry is tracked with a creation time (kept for diagnostics), but the
	// orphan gate is purely iteration-based now: no wall-clock fallback.
	d.mu.Lock()
	entry := d.activeDelegations["agent-1"]
	d.mu.Unlock()

	if entry == nil {
		t.Fatal("delegation not tracked")
	}
	if time.Since(entry.creationTime) > time.Second {
		t.Error("creation time should be very recent")
	}

	// Even though wall-clock time has passed, low iteration elapsed = no orphan.
	if msg := d.maybeWarnOrphanedDelegations(4); msg != "" {
		t.Errorf("iteration-based gate should not fire below threshold regardless of wall time, got: %s", msg)
	}
}

func TestDelegationState_OrphanNotTooTight(t *testing.T) {
	d := newDelegationState()

	// Spawn at iteration 2; the agent then does several iterations of its own
	// work before checking — that is normal, not an orphan.
	d.recordDelegationCall("agent-1", "spawn_agent", "Long task", "", 2)

	for iter := 3; iter < 2+orphanDelegationThreshold; iter++ {
		if msg := d.maybeWarnOrphanedDelegations(iter); msg != "" {
			t.Fatalf("no orphan expected at iteration %d (below threshold %d), got: %s", iter, orphanDelegationThreshold, msg)
		}
	}

	// At spawn+threshold elapsed iterations without any check, warn.
	if msg := d.maybeWarnOrphanedDelegations(2 + orphanDelegationThreshold); msg == "" {
		t.Fatal("expected orphan warning at threshold elapsed iterations")
	}
}

func TestDelegationState_FireAndForgetExemptFromOrphan(t *testing.T) {
	for _, tool := range []string{"swarm_task_create", "a2a_send_task"} {
		d := newDelegationState()
		d.recordDelegationCall("id-"+tool, tool, "Board task", "", 1)
		if len(d.activeDelegations) != 0 {
			t.Errorf("%s is fire-and-forget: must not be tracked as an orphan-able delegation", tool)
		}
		// Even many iterations later, no orphan warning.
		if msg := d.maybeWarnOrphanedDelegations(1 + orphanDelegationThreshold + 5); msg != "" {
			t.Errorf("%s should be exempt from orphan warnings, got: %s", tool, msg)
		}
	}
}

func TestDelegationState_SerialDelegationDetection(t *testing.T) {
	d := newDelegationState()

	// Two consecutive delegations - should not warn yet
	d.recordDelegationCall("a1", "spawn_agent", "Task 1", "", 1)
	msg := d.maybeWarnSerialDelegation()
	if msg != "" {
		t.Fatalf("expected no serial warning for 1 delegation, got: %s", msg)
	}

	d.recordDelegationCall("a2", "spawn_agent", "Task 2", "", 2)
	msg = d.maybeWarnSerialDelegation()
	if msg != "" {
		t.Fatalf("expected no serial warning for 2 delegations, got: %s", msg)
	}

	// Third consecutive delegation - should warn
	d.recordDelegationCall("a3", "spawn_agent", "Task 3", "", 3)
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
		d.recordDelegationCall("s"+itoaDel(i), "spawn_agent", "Serial "+itoaDel(i), "", i+1)
	}

	msg1 := d.maybeWarnSerialDelegation()
	if msg1 == "" {
		t.Fatal("expected first serial warning")
	}

	// Add more and check again
	for i := 0; i < serialDelegationThreshold; i++ {
		d.recordDelegationCall("t"+itoaDel(i), "spawn_agent", "Serial2 "+itoaDel(i), "", i+10)
	}
	_ = d.maybeWarnSerialDelegation()
	// Should still respect max warnings

	// Third attempt should be suppressed
	for i := 0; i < serialDelegationThreshold; i++ {
		d.recordDelegationCall("u"+itoaDel(i), "spawn_agent", "Serial3 "+itoaDel(i), "", i+20)
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
		d.recordDelegationCall("d"+itoaDel(i), "spawn_agent", "Delegate "+itoaDel(i), "", i)
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
	d.recordDelegationCall("d1", "spawn_agent", "One task", "", 1)
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
		d.recordDelegationCall("d"+itoaDel(i), "spawn_agent", "Task "+itoaDel(i), "", i)
		d.recordToolCallCount()
	}

	msg := d.maybeWarnOverDelegation()
	if msg != "" {
		t.Errorf("should not warn below min calls (%d), got: %s", overDelegationMinCalls, msg)
	}
}

func TestDelegationState_ResetForNewTurn(t *testing.T) {
	d := newDelegationState()

	d.recordDelegationCall("a1", "spawn_agent", "Task", "", 1)
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

	d.recordDelegationCall("agent-42", "spawn_agent", "To be removed", "", 1)
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
	tools := []string{"spawn_agent", "delegate", "send_message", "teammate_spawn",
		"use_namedagent", "a2a_remote"}

	for _, tool := range tools {
		d := newDelegationState()
		d.recordDelegationCall("id-"+tool, tool, "Task with "+tool, "", 1)
		if len(d.activeDelegations) != 1 {
			t.Errorf("tool %s not tracked as delegation", tool)
		}
	}

	// Fire-and-forget tools count as delegations (ratio/serial) but are not
	// tracked for orphan detection.
	for _, tool := range []string{"swarm_task_create", "a2a_send_task"} {
		d := newDelegationState()
		d.recordDelegationCall("id-"+tool, tool, "Task with "+tool, "", 1)
		if len(d.activeDelegations) != 0 {
			t.Errorf("fire-and-forget tool %s must not be orphan-tracked", tool)
		}
		if d.totalDelegations != 1 {
			t.Errorf("fire-and-forget tool %s should still count toward delegation ratio", tool)
		}
	}
}

func TestDelegationState_ResultToolsClearOrphan(t *testing.T) {
	// Survey tools report on ALL tracked delegations — no target needed.
	surveyTools := []string{"list_agents", "swarm_task_list", "a2a_list_tasks"}
	for _, tool := range surveyTools {
		d := newDelegationState()
		d.recordDelegationCall("toolu_01", "spawn_agent", "Task",
			"Sub-agent spawned with ID: agent-1", 1)
		d.recordResultCheck(tool, nil, "", 5)
		msg := d.maybeWarnOrphanedDelegations(6)
		if msg != "" {
			t.Errorf("survey tool %s did not clear orphan timer: %s", tool, msg)
		}
	}

	// Targeted tools must address the specific delegation by its returned ID.
	targetedTools := []string{"wait_agent", "task_output", "teammate_results", "a2a_get_task"}
	for _, tool := range targetedTools {
		d := newDelegationState()
		d.recordDelegationCall("toolu_01", "spawn_agent", "Task",
			"Sub-agent spawned with ID: agent-1", 1)
		d.recordResultCheck(tool, json.RawMessage(`{"agent_id":"agent-1","task_id":"agent-1","teammate_id":"agent-1"}`), "", 5)
		// Check at iteration 6: elapsed = 6-5 = 1, which is below threshold
		msg := d.maybeWarnOrphanedDelegations(6)
		if msg != "" {
			t.Errorf("targeted result tool %s did not clear addressed orphan timer: %s", tool, msg)
		}
	}
}

// TestParseDelegationResultID verifies agent ID extraction from delegation
// tool results — the bridge between the tool-call ID and agent ID namespaces (#348).
func TestParseDelegationResultID(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"spawn_agent result", "Sub-agent spawned with ID: sa-9\nUse wait_agent or list_agents to monitor progress and retrieve the result.", "sa-9"},
		{"agent ID", "Agent started with ID: agent-3", "agent-3"},
		{"no ID", "Spawned a thing with no identifier", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := parseDelegationResultID(c.content); got != c.want {
			t.Errorf("%s: parseDelegationResultID(%q) = %q, want %q", c.name, c.content, got, c.want)
		}
	}
}

// TestDelegationState_GenericFieldsNotTarget verifies that generic argument
// fields ("name", "agent") no longer resolve as delegation targets —
// read_command_output {"name":"build"} must not reset a foreign delegation's
// orphan timer just because its summary mentions "build" (#348).
func TestDelegationState_GenericFieldsNotTarget(t *testing.T) {
	d := newDelegationState()
	d.recordDelegationCall("toolu_01", "spawn_agent", "Task about build system",
		"Sub-agent spawned with ID: sa-1", 1)

	// read_command_output with a "name" arg that matches words in the summary
	d.recordResultCheck("read_command_output", json.RawMessage(`{"name":"build"}`), "", 5)

	d.mu.Lock()
	last := d.activeDelegations["toolu_01"].lastChecked
	d.mu.Unlock()
	if last != 1 {
		t.Errorf("generic name field must not mark delegation, lastChecked=%d want 1", last)
	}
}

// TestDelegationState_ResultContentFallbackMatchesResultID verifies the
// content fallback uses the parsed result ID (agent namespace), not just the
// tool-call ID (#348).
func TestDelegationState_ResultContentFallbackMatchesResultID(t *testing.T) {
	d := newDelegationState()
	d.recordDelegationCall("toolu_01", "spawn_agent", "Task",
		"Sub-agent spawned with ID: sa-9", 1)

	// list_agents-style result naming agent ID sa-9 (not toolu_01)
	d.recordResultCheck("list_agents", json.RawMessage(`{}`), "sa-9: completed (idle)", 5)

	d.mu.Lock()
	last := d.activeDelegations["toolu_01"].lastChecked
	d.mu.Unlock()
	if last != 5 {
		t.Errorf("content fallback should match resultID, lastChecked=%d want 5", last)
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
