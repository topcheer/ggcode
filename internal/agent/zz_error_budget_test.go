package agent

import (
	"strings"
	"testing"
)

func TestErrorBudgetFastBurnWarnsOnce(t *testing.T) {
	e := newErrorBudget()
	// SLO 0.85 => nominal failure budget 15%; fast burn at >30%.
	for i := 0; i < 6; i++ {
		if msg := e.evaluate(true); msg != "" {
			t.Fatalf("successes must stay quiet, got %q", msg)
		}
	}
	warnMsg := ""
	for i := 0; i < 3; i++ {
		msg := e.evaluate(false)
		if msg != "" && i < 2 {
			t.Fatalf("warn must fire exactly at 9th sample, fired early at %d", 7+i)
		}
		if msg != "" {
			warnMsg = msg
		}
	}
	// 9th sample (i=2): 3/9 = 33% > 30% => warn consumed there; a further
	// failure must NOT re-warn (fires once).
	if !strings.Contains(warnMsg, "fast burn") {
		t.Fatalf("fast burn warn expected at 9th sample (3/9=33%%), got %q", warnMsg)
	}
	if msg := e.evaluate(false); msg != "" {
		t.Fatalf("warn must fire once, got %q", msg)
	}
}

func TestErrorBudgetTripsAndStaysQuiet(t *testing.T) {
	e := newErrorBudget()
	var got string
	for i := 0; i < 20; i++ {
		success := i%3 != 0 // 1/3 failure rate: fails>=6 reachable
		if msg := e.evaluate(success); msg != "" {
			got = msg
		}
	}
	if got == "" || !strings.Contains(got, "BUDGET EXHAUSTED") || !strings.Contains(got, "ask_user") {
		t.Fatalf("trip expected with 75%% failure, last msg: %q", got)
	}
	// After tripping, stay quiet (escalation already requested).
	for i := 0; i < 5; i++ {
		if msg := e.evaluate(false); msg != "" {
			t.Fatalf("tripped budget must stay quiet, got %q", msg)
		}
	}
	if e.burnRate() < 0.5 {
		t.Fatalf("burn rate must reflect failures, got %.2f", e.burnRate())
	}
}

func TestErrorBudgetHealthyNeverFires(t *testing.T) {
	e := newErrorBudget()
	for i := 0; i < 100; i++ {
		success := i%10 != 0 // 90% success > SLO 85%
		if msg := e.evaluate(success); msg != "" {
			t.Fatalf("healthy stream must stay quiet at i=%d, got %q", i, msg)
		}
	}
}

func TestErrorBudgetResetClears(t *testing.T) {
	e := newErrorBudget()
	for i := 0; i < 20; i++ {
		e.evaluate(false)
	}
	e.reset()
	if e.burnRate() != 0 {
		t.Fatalf("reset must clear ring, burnRate=%.2f", e.burnRate())
	}
	if msg := e.evaluate(true); msg != "" {
		t.Fatalf("post-reset must be quiet, got %q", msg)
	}
}
