package agent

import (
	"strings"
	"testing"
)

// Characterization test: a FAITHFUL agent that declares a 3-step plan, then
// narrates executing every step (past tense, one per turn), and only then
// claims legitimate completion — the plan-abandon detector STILL fires.
//
// This documents the false-positive shape analyzed in the plan_abandon bug
// verification: maybeWarnPlanAbandon consumes only assistant text and has no
// step-execution evidence path, so "declared plan, later claimed done" is
// indistinguishable from "declared plan, skipped steps, claimed done".
func TestPlanAbandonFaithfulExecutionFalsePositive(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}

	// Turn 1: declare the plan (3 future-tense numbered steps).
	plan := "Here's my plan:\n" +
		"1. I'll read the auth module\n" +
		"2. Next, I'll fix the token refresh logic\n" +
		"3. Finally, I'll run the tests to verify"
	if hint := a.maybeWarnPlanAbandon(plan); hint != "" {
		t.Fatalf("expected no warning on plan declaration, got: %s", hint)
	}

	// Turns 2-4: execute each step, narrating in past tense.
	// None of these contain a planCompletionRe match, so no early trigger.
	execution := []string{
		"I read the auth module; the refresh path is in token.go line 42.",
		"I fixed the token refresh logic and updated both callers.",
		"I ran the test suite: all 12 tests pass.",
	}
	for i, txt := range execution {
		if hint := a.maybeWarnPlanAbandon(txt); hint != "" {
			t.Fatalf("turn %d: unexpected warning during execution: %s", i+2, hint)
		}
	}

	// Turn 5: legitimate completion claim AFTER all steps executed.
	hint := a.maybeWarnPlanAbandon("The task is now complete.")
	if hint == "" {
		t.Fatal("bug not reproduced: expected the false-positive warning on faithful completion, got none")
	}
	if !strings.Contains(hint, "[plan-abandon]") {
		t.Fatalf("expected [plan-abandon] tag in warning, got: %s", hint)
	}
	t.Logf("false positive confirmed on fully-executed plan: %s", hint)
}
