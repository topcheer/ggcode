package agent

import (
	"strings"
	"testing"
)

func TestIssue1170SmallEditDoesNotResetErrorExpiry(t *testing.T) {
	s := newOvercorrectionState()
	s.pendingErr = severityTrivial

	for round := 0; round < 3*overcorrectionMaxErrorAge; round++ {
		if hint := s.recordEdit(overcorrectionMinEditBytes-1, "a.go"); hint != "" {
			t.Fatalf("small edit must never warn, round %d: %s", round, hint)
		}
		s.recordNonEditStep()
	}

	if s.pendingErr != severityNone {
		t.Fatalf("stale pending error should expire after %d non-edit steps, still %v",
			overcorrectionMaxErrorAge, s.pendingErr)
	}

	if hint := s.recordEdit(20000, "a.go"); hint != "" {
		t.Fatalf("expired error must not flag later edits: %s", hint)
	}
}

func TestIssue1170SmallEditDoesNotBreakCascadeStreak(t *testing.T) {
	s := newOvercorrectionState()

	s.pendingErr = severityTrivial
	if hint := s.recordEdit(6000, "a.go"); hint == "" {
		t.Fatal("expected single overcorrection warning")
	}

	if hint := s.recordEdit(overcorrectionMinEditBytes-1, "a.go"); hint != "" {
		t.Fatalf("small edit must never warn: %s", hint)
	}

	s.pendingErr = severityTrivial
	hint := s.recordEdit(7000, "b.go")
	if hint == "" {
		t.Fatal("expected cascade warning for second overcorrection")
	}
	if !strings.Contains(hint, "overcorrections") {
		t.Fatalf("expected cascade warning, got single-edit warning: %s", hint)
	}
}

func TestIssue1170AssessableNonOvercorrectionBreaksStreak(t *testing.T) {
	s := newOvercorrectionState()

	s.pendingErr = severityModerate
	if hint := s.recordEdit(8000, "a.go"); hint != "" {
		t.Fatalf("moderate single overcorrection should not warn alone: %s", hint)
	}

	s.pendingErr = severityModerate
	if hint := s.recordEdit(600, "b.go"); hint != "" {
		t.Fatalf("proportional fix should not warn: %s", hint)
	}

	s.pendingErr = severityModerate
	if hint := s.recordEdit(8000, "c.go"); hint != "" {
		t.Fatalf("assessable non-overcorrected edit must break the cascade streak, got: %s", hint)
	}
}
