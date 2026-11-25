package agent

import "testing"

func TestToolCallBudget_NoBudgetNeverStops(t *testing.T) {
	b := newToolCallBudget()
	for i := 0; i < 1000; i++ {
		b.record()
		if b.check() {
			t.Fatalf("no explicit budget: must never stop, stopped at call %d", i+1)
		}
	}
}

func TestToolCallBudget_ExplicitBudgetStopsExactlyOnce(t *testing.T) {
	b := newToolCallBudget()
	b.SetBudget(3)
	for i := 0; i < 2; i++ {
		b.record()
		if b.check() {
			t.Fatalf("stopped before the budget at call %d", i+1)
		}
	}
	b.record()
	if !b.check() {
		t.Fatal("budget reached: must report exhausted")
	}
	// The gate reports once; the caller ends the run on the first report.
	b.record()
	if b.check() {
		t.Fatal("must report exhaustion only once")
	}
	b.reset()
	b.record()
	if b.check() {
		t.Fatal("reset must clear the counter and the latch")
	}
}

func TestToolCallBudget_ZeroOrNegativeBudgetDisables(t *testing.T) {
	for _, budget := range []int{0, -5} {
		b := newToolCallBudget()
		b.SetBudget(budget)
		b.record()
		if b.check() {
			t.Fatalf("budget %d must disable the gate", budget)
		}
	}
}
