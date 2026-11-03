package agent

import "testing"

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

	ca.warnings = 0
	ca.markUndelivered()
	if ca.warnings != 0 {
		t.Fatal("constraintAmnesia: markUndelivered must floor at 0")
	}
}
