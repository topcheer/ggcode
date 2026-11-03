package agent

import "testing"

func TestIssue4241_IndicatorWindowRadix(t *testing.T) {
	cd := newCriteriaDriftState()

	cd.recordAssistantText("The requirement is really only the happy path.", 0)
	cd.recordAssistantText("The requirement is really only the fast path.", 3)
	if len(cd.indicators) < 2 {
		t.Fatalf("expected >=2 indicators recorded, got %d", len(cd.indicators))
	}
	for _, ind := range cd.indicators {
		if ind.iter == 0 {
			t.Fatal("recorded iter must be normalized to 1-based (got 0)")
		}
	}
}

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
