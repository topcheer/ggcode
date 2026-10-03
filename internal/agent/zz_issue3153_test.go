package agent

import (
	"strings"
	"testing"
)

// TestIssue3153_WarnedClearedOnSuccess pins the recurrent-failure warning path:
// 3 fails -> warning -> 1 success -> 3 more fails must warn again (previously
// the stale warned flag sent the second streak into a silent dead zone until
// the forced threshold of 6).
func TestIssue3153_WarnedClearedOnSuccess(t *testing.T) {
	p := newPivotDecisionTracker()
	args := `{"command":"go test ./pkg/"}`
	const tool = "run_command"

	fail := func() { p.recordToolCall(tool, args, true, "exit status 1") }
	ok := func() { p.recordToolCall(tool, args, false, "ok") }

	// First streak: threshold reached -> first warning.
	for i := 0; i < pivotFirstThreshold; i++ {
		fail()
	}
	first := p.checkAndWarn()
	if first == "" || !strings.Contains(first, "pivot-decision") {
		t.Fatalf("first streak should warn, got %q", first)
	}
	if !strings.Contains(first, "REPAIR") {
		t.Fatalf("first warning should be the REPAIR-vs-PIVOT prompt, got %q", first)
	}

	// Success clears both the streak and (post-#3153) the warned flag.
	ok()

	// Second streak: same length must warn again - not stay silent.
	for i := 0; i < pivotFirstThreshold; i++ {
		fail()
	}
	second := p.checkAndWarn()
	if second == "" {
		t.Fatal("recurrent failure streak hit silent dead zone: no warning after success-reset (issue 3153)")
	}
	if !strings.Contains(second, "REPAIR") {
		t.Fatalf("recurrent warning should restart at the decision prompt, got %q", second)
	}
}

// TestIssue3153_ForcedPathStillWorks ensures the forced escalation (second
// emission at the forced threshold) is unaffected by the fix: no success in
// between, streak climbs 3 -> 6.
func TestIssue3153_ForcedPathStillWorks(t *testing.T) {
	p := newPivotDecisionTracker()
	args := `{"command":"make verify"}`
	const tool = "run_command"

	for i := 0; i < pivotFirstThreshold; i++ {
		p.recordToolCall(tool, args, true, "exit status 2")
	}
	if w := p.checkAndWarn(); w == "" || !strings.Contains(w, "REPAIR") {
		t.Fatalf("expected first warning, got %q", w)
	}
	for i := 0; i < pivotForcedThreshold-pivotFirstThreshold; i++ {
		p.recordToolCall(tool, args, true, "exit status 2")
	}
	w := p.checkAndWarn()
	if w == "" || !strings.Contains(w, "PIVOT") {
		t.Fatalf("expected forced PIVOT at threshold %d, got %q", pivotForcedThreshold, w)
	}
}
