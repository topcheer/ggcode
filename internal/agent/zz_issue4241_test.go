package agent

import "testing"

// #4241: record side is 0-based while maybeWarn's call site is 1-based; the
// mixed radix narrowed the indicator window by one turn. After the fix the
// recorded iter is normalized to 1-based, so a distance-3 pair must merge.
func TestIssue4241_IndicatorWindowRadix(t *testing.T) {
	cd := newCriteriaDriftState()
	// Real trigger phrase (same as the existing test suite uses).
	cd.recordAssistantText("The requirement is really only the happy path.", 0) // 0-based call-site
	cd.recordAssistantText("The requirement is really only the fast path.", 3)  // distance 3
	if len(cd.indicators) < 2 {
		t.Fatalf("expected >=2 indicators recorded, got %d", len(cd.indicators))
	}
	for _, ind := range cd.indicators {
		if ind.iter == 0 {
			t.Fatal("recorded iter must be normalized to 1-based (got 0)")
		}
	}
}

// #4242: pure read-only runs must NOT get the edit-scoped reminder; an edit
// within the recency window must re-enable it.
func TestIssue4242_EditRelevanceGate(t *testing.T) {
	ca := newConstraintAmnesiaState()
	ca.recordConstraints("do not modify the auth module", 1)
	if got := ca.maybeWarn(20); got != "" {
		t.Fatalf("read-only run at iter 20 must not fire, got %q", got)
	}
	ca.noteEdit(19)
	if got := ca.maybeWarn(20); got == "" {
		t.Fatal("edit one turn ago must re-enable the reminder")
	}
	ca2 := newConstraintAmnesiaState()
	ca2.recordConstraints("never touch vendor/", 1)
	ca2.noteEdit(5)
	if got := ca2.maybeWarn(20); got != "" {
		t.Fatal("edit 15 turns ago is outside the recency window - must stay silent")
	}
}
