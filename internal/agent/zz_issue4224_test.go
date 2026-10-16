package agent

import "testing"

// #4224: detectors wired through guidanceEmit used to burn their one-shot
// quota before delivery; when the guidance budget suppressed the injection
// the message was lost AND the quota was gone (silent permanent miss). The
// #3380 explicit pattern + markUndelivered must roll the quota back.
func TestIssue4224_MarkUndeliveredRollsBackQuota(t *testing.T) {
	sg := newSubgoalState()
	sg.fired = true
	sg.markUndelivered()
	if sg.fired {
		t.Fatal("subgoalTrack: markUndelivered must clear fired")
	}

	sd := newSuccessDeclareState()
	sd.fired = true
	sd.warnCount = 1
	sd.markUndelivered()
	if sd.fired || sd.warnCount != 0 {
		t.Fatalf("successDeclare: markUndelivered must clear fired and decrement warnCount, got fired=%v warnCount=%d", sd.fired, sd.warnCount)
	}

	ca := newConstraintAmnesiaState()
	ca.warnings = 1
	ca.markUndelivered()
	if ca.warnings != 0 {
		t.Fatalf("constraintAmnesia: markUndelivered must decrement warnings, got %d", ca.warnings)
	}
	// Floor: never negative.
	ca.warnings = 0
	ca.markUndelivered()
	if ca.warnings != 0 {
		t.Fatal("constraintAmnesia: markUndelivered must floor at 0")
	}
}
