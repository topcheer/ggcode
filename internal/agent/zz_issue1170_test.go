package agent

import (
	"strings"
	"testing"
)

// Issue #1170 fix A: a below-threshold edit must not reset stepsSinceError.
// Resetting it there kept a stale pending error alive forever, because only
// recordNonEditStep advances the counter and the small edit wiped its progress
// every cycle.
func TestIssue1170SmallEditDoesNotResetErrorExpiry(t *testing.T) {
	s := newOvercorrectionState()
	s.pendingErr = severityTrivial

	// Each round: one small (non-assessable) edit plus one non-edit step.
	// Under the buggy code the small edit reset the counter to 0 and the
	// non-edit step only ever brought it back to 1, so the pending error
	// never expired via overcorrectionMaxErrorAge.
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

	// After expiry a large edit must not be flagged against the dead error.
	if hint := s.recordEdit(20000, "a.go"); hint != "" {
		t.Fatalf("expired error must not flag later edits: %s", hint)
	}
}

// Issue #1170 fix B: a below-threshold edit interleaved between overcorrections
// must not break the cascade streak. Previously the overcorrected:false entry
// ended the consecutive scan, so cascades were systematically under-counted.
func TestIssue1170SmallEditDoesNotBreakCascadeStreak(t *testing.T) {
	s := newOvercorrectionState()

	// Overcorrection #1: trivial error, massive fix.
	s.pendingErr = severityTrivial
	if hint := s.recordEdit(6000, "a.go"); hint == "" {
		t.Fatal("expected single overcorrection warning")
	}

	// Small interleaved edit (common real-world cascade shape): must not end
	// the streak.
	if hint := s.recordEdit(overcorrectionMinEditBytes-1, "a.go"); hint != "" {
		t.Fatalf("small edit must never warn: %s", hint)
	}

	// Overcorrection #2 must be reported as a cascade.
	s.pendingErr = severityTrivial
	hint := s.recordEdit(7000, "b.go")
	if hint == "" {
		t.Fatal("expected cascade warning for second overcorrection")
	}
	if !strings.Contains(hint, "overcorrections") {
		t.Fatalf("expected cascade warning, got single-edit warning: %s", hint)
	}
}

// Issue #1170 companion: an assessable, non-overcorrected edit (>= minimum
// size and proportional to the error) still ends the streak. Only small edits
// are transparent to cascade counting.
func TestIssue1170AssessableNonOvercorrectionBreaksStreak(t *testing.T) {
	s := newOvercorrectionState()

	// Overcorrection #1: moderate error, 8000 bytes (8000/400 = 20 > 15).
	s.pendingErr = severityModerate
	if hint := s.recordEdit(8000, "a.go"); hint != "" {
		t.Fatalf("moderate single overcorrection should not warn alone: %s", hint)
	}

	// Assessable, proportional fix: 600 bytes for a moderate error is not an
	// overcorrection and must reset the streak.
	s.pendingErr = severityModerate
	if hint := s.recordEdit(600, "b.go"); hint != "" {
		t.Fatalf("proportional fix should not warn: %s", hint)
	}

	// Another overcorrection now has a streak of 1: no cascade, and moderate
	// severity alone never produces a single warning.
	s.pendingErr = severityModerate
	if hint := s.recordEdit(8000, "c.go"); hint != "" {
		t.Fatalf("assessable non-overcorrected edit must break the cascade streak, got: %s", hint)
	}
}
