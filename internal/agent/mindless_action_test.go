package agent

import "testing"

func TestMindlessActionState_BelowStreakThreshold(t *testing.T) {
	s := newMindlessActionState()

	for i := 0; i < 3; i++ {
		if s.recordStep(10, true) {
			t.Fatalf("step %d: should not trigger with streak %d < %d", i+1, s.streak, mindlessStreakThreshold)
		}
	}
	if s.streak != 3 {
		t.Errorf("expected streak=3, got %d", s.streak)
	}
}

func TestMindlessActionState_AtStreakThreshold(t *testing.T) {
	s := newMindlessActionState()

	for i := 0; i < 3; i++ {
		s.recordStep(10, true)
	}

	if !s.recordStep(10, true) {
		t.Fatal("expected trigger at streak=4")
	}
}

func TestMindlessActionState_AdequateReasoningBreaksStreak(t *testing.T) {
	s := newMindlessActionState()

	for i := 0; i < 3; i++ {
		s.recordStep(10, true)
	}

	s.recordStep(200, true)
	if s.streak != 0 {
		t.Errorf("expected streak=0 after adequate reasoning, got %d", s.streak)
	}

	if s.recordStep(10, true) {
		t.Fatal("should not trigger: streak was reset")
	}
}

func TestMindlessActionState_NonToolStepResetsStreak(t *testing.T) {
	s := newMindlessActionState()

	for i := 0; i < 3; i++ {
		s.recordStep(10, true)
	}

	s.recordStep(0, false)
	if s.streak != 0 {
		t.Errorf("expected streak=0 after non-tool step, got %d", s.streak)
	}
}

func TestMindlessActionState_MaxWarnings(t *testing.T) {
	s := newMindlessActionState()

	for i := 0; i < 4; i++ {
		s.recordStep(10, true)
	}
	if s.warnings != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warnings)
	}

	for i := 0; i < 4; i++ {
		s.recordStep(10, true)
	}
	if s.warnings != 2 {
		t.Fatalf("expected 2 warnings, got %d", s.warnings)
	}

	for i := 0; i < 4; i++ {
		if s.recordStep(10, true) {
			t.Fatal("should not trigger after max warnings exceeded")
		}
	}
}

func TestMindlessActionState_Reset(t *testing.T) {
	s := newMindlessActionState()
	s.recordStep(10, true)
	s.recordStep(10, true)
	s.warnings = 1
	s.reset()
	if s.streak != 0 || s.warnings != 0 {
		t.Errorf("reset failed: streak=%d warnings=%d", s.streak, s.warnings)
	}
}

func TestMindlessActionWarning_Format(t *testing.T) {
	msg := mindlessActionWarning(5)
	if msg == "" {
		t.Fatal("expected non-empty warning")
	}
	if !contains(msg, "5") {
		t.Errorf("warning should contain streak count '5': %s", msg)
	}
	if !contains(msg, "mindless-action") {
		t.Errorf("warning should contain detector tag: %s", msg)
	}
}
