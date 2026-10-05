package permission

import (
	"context"
	"errors"
	"testing"
)

// #3370: approval handlers classify a fired ctx.Done() into a non-decision
// outcome - deadline means the prompt expired; anything else is a
// cancellation. Neither may ever be attributed to the user downstream.
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

// String labels must be stable for logs/audit.
func TestDecisionStringNewValues(t *testing.T) {
	if Timeout.String() != "timeout" || Cancelled.String() != "cancelled" {
		t.Errorf("String labels drifted: timeout=%q cancelled=%q", Timeout.String(), Cancelled.String())
	}
}
