package agent

// #638: the production wiring calls startIteration(i+1) — which pre-appends
// an empty record for the iteration that is only starting — immediately
// before checkMomentumLoss. The trailing empty record occupied a stall-window
// slot that could never satisfy `productive == 0 && total > 0`, so with
// momentumStallWindow == 2 the detector could never fire on the live path
// (only hand-built records in unit tests reached the fire branch). These
// tests replay the exact production call order.

import "testing"

// Replay of the ver-106 wiring probe: one productive iteration followed by
// stalled terminal-phase iterations, checked in production order
// (startIteration -> check -> recordToolCall...).
func TestIssue638_MomentumLoss_FiresUnderProductionWiringOrder(t *testing.T) {
	m := newMomentumLossState()
	const maxIter = 10

	// Iteration 1: productive work happens AFTER the check (production order).
	m.startIteration(1)
	if msg := m.checkMomentumLoss(maxIter); msg != "" {
		t.Fatalf("iteration 1 must not fire, got: %s", msg)
	}
	m.recordToolCall("edit_file", nil)
	m.recordToolCall("write_file", nil)

	// Iterations 6-7: stalled terminal-phase iterations (read-only activity).
	for iter := 6; iter <= 7; iter++ {
		m.startIteration(iter)
		if msg := m.checkMomentumLoss(maxIter); msg != "" {
			t.Fatalf("iteration %d fired too early (only one completed stall), got: %s", iter, msg)
		}
		m.recordToolCall("read_file", nil)
		m.recordToolCall("grep", nil)
	}

	// Iteration 8's check runs right after startIteration(8) pre-appends the
	// empty record. With the #638 fix the trailing empty record is skipped,
	// the window sees the two COMPLETED stalled iterations (6 and 7), and the
	// detector FIRES — this is the first point the production wiring can
	// detect the stall (before the fix this check could never fire).
	m.startIteration(8)
	if msg := m.checkMomentumLoss(maxIter); msg == "" {
		t.Fatal("#638 regression: momentum loss never fires under production wiring order")
	}
	if !m.fired {
		t.Fatal("expected fired flag set")
	}
}

// The check made at the END of an iteration (no trailing empty record) must
// keep firing — the skip logic must not disable the original path.
func TestIssue638_MomentumLoss_StillFiresWithoutTrailingEmptyRecord(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(7)
	m.recordToolCall("read_file", nil)
	m.startIteration(8)
	m.recordToolCall("search_files", nil)
	if msg := m.checkMomentumLoss(10); msg == "" {
		t.Fatal("expected fire with only completed iterations in the window")
	}
}

// A trailing empty record followed by a PRODUCTIVE completed iteration must
// not fire — skipping the empty record must not create a false stall signal
// where the previous iteration was productive.
func TestIssue638_MomentumLoss_TrailingEmptyPlusProductiveNoFire(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(7)
	m.recordToolCall("edit_file", nil) // productive
	m.startIteration(8)                // empty (just started)
	if msg := m.checkMomentumLoss(10); msg != "" {
		t.Fatalf("no stall: only 0 completed unproductive iterations, got: %s", msg)
	}
}

// All-empty trailing records (agent done, no activity) must stay silent.
func TestIssue638_MomentumLoss_AllTrailingEmptyNoFire(t *testing.T) {
	m := newMomentumLossState()
	m.startIteration(1)
	m.recordToolCall("edit_file", nil)
	m.startIteration(7)
	m.startIteration(8)
	m.startIteration(9)
	if msg := m.checkMomentumLoss(10); msg != "" {
		t.Fatalf("no recent activity must not fire, got: %s", msg)
	}
}
