package agent

import (
	"testing"
)

func TestMomentumLoss_NoFire_InsufficientHistory(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(2)
	m.recordToolCall("read_file", nil)

	// Only 2 iterations -- below minimum
	msg := m.checkMomentumLoss(10)
	if msg != "" {
		t.Fatalf("expected no message with < 3 iterations, got: %s", msg)
	}
}

func TestMomentumLoss_NoFire_NotInTerminalPhase(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(2)
	m.recordToolCall("read_file", nil)
	m.startIteration(3)
	m.recordToolCall("read_file", nil)

	// Terminal phase starts at 60% of 10 = iteration 6
	// We're at iteration 3
	msg := m.checkMomentumLoss(10)
	if msg != "" {
		t.Fatalf("expected no message before terminal phase, got: %s", msg)
	}
}

func TestMomentumLoss_NoFire_NoPriorProductivity(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("read_file", nil)
	m.startIteration(2)
	m.recordToolCall("grep", nil)
	m.startIteration(3)
	m.recordToolCall("read_file", nil)
	m.startIteration(4)
	m.recordToolCall("read_file", nil)
	m.startIteration(5)
	m.recordToolCall("read_file", nil)
	m.startIteration(6)
	m.recordToolCall("read_file", nil)
	m.startIteration(7)
	m.recordToolCall("read_file", nil)

	// In terminal phase (7 >= 6) but no prior productive work
	msg := m.checkMomentumLoss(10)
	if msg != "" {
		t.Fatalf("expected no message without prior productivity, got: %s", msg)
	}
}

func TestMomentumLoss_Fires_OnStallAfterProductivity(t *testing.T) {
	m := newMomentumLossState()
	// Early: productive
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.recordToolCall("write_file", nil)
	// Mid: productive
	m.startIteration(2)
	m.recordToolCall("run_command", nil)
	// Terminal: all read-only
	m.startIteration(7)
	m.recordToolCall("read_file", nil)
	m.recordToolCall("grep", nil)
	m.startIteration(8)
	m.recordToolCall("search_files", nil)
	m.recordToolCall("read_file", nil)

	msg := m.checkMomentumLoss(10)
	if msg == "" {
		t.Fatal("expected momentum loss message on stall after productivity")
	}
	if m.fired != true {
		t.Fatal("expected fired flag to be set")
	}
}

func TestMomentumLoss_NoFire_ProductivityContinues(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(7)
	m.recordToolCall("edit_file", nil)
	m.startIteration(8)
	m.recordToolCall("read_file", nil)

	// Only 1 stall iteration (need 2)
	msg := m.checkMomentumLoss(10)
	if msg != "" {
		t.Fatalf("expected no message when productivity continues, got: %s", msg)
	}
}

func TestMomentumLoss_NoFire_NoRecentActivity(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	// Iterations with no tool calls at all -- agent is done
	m.startIteration(7)
	m.startIteration(8)

	msg := m.checkMomentumLoss(10)
	if msg != "" {
		t.Fatalf("expected no message when no recent activity, got: %s", msg)
	}
}

func TestMomentumLoss_FiresOnce(t *testing.T) {
	m := newMomentumLossState()
	m.fired = true

	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(7)
	m.recordToolCall("read_file", nil)
	m.startIteration(8)
	m.recordToolCall("read_file", nil)

	msg := m.checkMomentumLoss(10)
	if msg != "" {
		t.Fatalf("expected no message after already fired, got: %s", msg)
	}
}

func TestMomentumLoss_Reset(t *testing.T) {
	m := newMomentumLossState()
	m.fired = true
	m.iterations = append(m.iterations, momentumIterRecord{iter: 1, productive: 1})
	m.currentIter = 5

	m.reset()
	if m.fired != false {
		t.Fatal("expected fired=false after reset")
	}
	if len(m.iterations) != 0 {
		t.Fatal("expected empty iterations after reset")
	}
	if m.currentIter != 0 {
		t.Fatal("expected currentIter=0 after reset")
	}
}

func TestMomentumLoss_StartIteration_Idempotent(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(3)
	m.startIteration(3) // should not create duplicate
	if len(m.iterations) != 1 {
		t.Fatalf("expected 1 iteration record, got %d", len(m.iterations))
	}
}

func TestMomentumLoss_RecordToolCall_NoIterations(t *testing.T) {
	m := newMomentumLossState()
	// Should not panic
	m.recordToolCall("edit_file", nil)
}

func TestIsMomentumProductiveTool(t *testing.T) {
	productive := []string{"edit_file", "write_file", "run_command", "git_commit", "multi_edit_file"}
	for _, name := range productive {
		if !isMomentumProductiveTool(name) {
			t.Errorf("expected %s to be productive", name)
		}
	}
	consumptive := []string{"read_file", "grep", "search_files", "glob", "lsp_hover"}
	for _, name := range consumptive {
		if isMomentumProductiveTool(name) {
			t.Errorf("expected %s to NOT be productive", name)
		}
	}
}

func TestHasRecentProductiveActivity(t *testing.T) {
	records := []momentumIterRecord{
		{iter: 1, productive: 2},
		{iter: 2, productive: 0},
		{iter: 3, productive: 0},
	}
	if hasRecentProductiveActivity(records, 2) {
		t.Fatal("expected no recent productive activity in last 2")
	}
	records[2].productive = 1
	if !hasRecentProductiveActivity(records, 2) {
		t.Fatal("expected recent productive activity in last 2")
	}
}
