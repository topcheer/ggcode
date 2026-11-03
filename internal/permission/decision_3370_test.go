package permission

import (
	"context"
	"errors"
	"testing"
)

func TestDecisionFromContext(t *testing.T) {
	if got := DecisionFromContext(context.DeadlineExceeded); got != Timeout {
		t.Errorf("DeadlineExceeded -> %v, want Timeout", got)
	}
	if got := DecisionFromContext(context.Canceled); got != Cancelled {
		t.Errorf("Canceled -> %v, want Cancelled", got)
	}
	if got := DecisionFromContext(errors.New("other")); got != Cancelled {
		t.Errorf("other error -> %v, want Cancelled (fail toward cancellation)", got)
	}
	if got := DecisionFromContext(nil); got != Cancelled {
		t.Errorf("nil error -> %v, want Cancelled", got)
	}
}

func TestDecisionIsNonDecision(t *testing.T) {
	for _, d := range []Decision{Timeout, Cancelled} {
		if !d.IsNonDecision() {
			t.Errorf("%v.IsNonDecision() = false, want true", d)
		}
	}
	for _, d := range []Decision{Allow, Deny, Ask} {
		if d.IsNonDecision() {
			t.Errorf("%v.IsNonDecision() = true, want false", d)
		}
	}
}

func TestDecisionStringNewValues(t *testing.T) {
	if Timeout.String() != "timeout" || Cancelled.String() != "cancelled" {
		t.Errorf("String labels drifted: timeout=%q cancelled=%q", Timeout.String(), Cancelled.String())
	}
}
