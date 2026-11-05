package agent

// #5013 companion: the exec-layer per-invocation budget must hard-stop a
// run whose tool-call count exceeds the budget even when the overshoot
// happens inside a single parallel batch (the turn cap cannot see it).

import (
	"strings"
	"testing"
)

func TestIssue5013BudgetStopsAfterParallelBatchOvershoot(t *testing.T) {
	b := newToolCallBudget()
	b.SetBudget(3)

	// One LLM turn issued 5 tool calls in parallel; record() runs per
	// invocation at the exec join point (agent.go per-tc loop).
	for i := 0; i < 5; i++ {
		b.record()
	}

	msg, stop := b.check()
	if !stop {
		t.Fatal("check() after 5 calls against budget 3 must hard-stop")
	}
	if !strings.Contains(msg, "5 / 3") {
		t.Fatalf("stop message = %q, want counts \"5 / 3\"", msg)
	}

	// Stop is latched: subsequent checks do not re-fire a second stop.
	if _, stopAgain := b.check(); stopAgain {
		t.Fatal("check() must latch the stop (single stop event)")
	}
}

func TestIssue5013ExplicitBudgetOverridesTurnDerivedDefault(t *testing.T) {
	b := newToolCallBudget()
	b.SetDefaultBudget(deriveDefaultBudget(60)) // turn-derived default: 480
	if b.effectiveBudget() != 480 {
		t.Fatalf("default effective budget = %d, want 480", b.effectiveBudget())
	}

	// A spawn-supplied max_tool_calls must win over the turn-derived default
	// (runner.go wires it via SetToolCallBudget).
	b.SetBudget(12)
	if b.effectiveBudget() != 12 {
		t.Fatalf("explicit effective budget = %d, want 12", b.effectiveBudget())
	}
}

func TestIssue5013BudgetZeroMeansNoStop(t *testing.T) {
	b := newToolCallBudget()
	for i := 0; i < 100; i++ {
		b.record()
	}
	if msg, stop := b.check(); stop || msg != "" {
		t.Fatalf("unset budget must never stop: got stop=%v msg=%q", stop, msg)
	}
}
