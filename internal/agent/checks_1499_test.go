package agent

import (
	"testing"
)

// #1499 case A pin: a diagnostic numbered list without plan intent is not
// adopted as a plan.
func Test1499DiagnosticListNotPlan(t *testing.T) {
	s := newSubgoalState()
	diag := "Found 3 problems:\n1. missing nil check in foo\n2. off-by-one in bar loop\n3. unhandled error in baz\n"
	s.recordAssistantText(diag, 1)
	if len(s.subgoals) != 0 {
		t.Fatalf("diagnostic list must not become a plan (got %d subgoals)", len(s.subgoals))
	}
	// Plan-intent text IS adopted.
	s2 := newSubgoalState()
	plan := "My plan:\n1. fix nil check in foo\n2. fix off-by-one in bar loop\n3. handle error in baz\n"
	s2.recordAssistantText(plan, 1)
	if len(s2.subgoals) == 0 {
		t.Fatal("plan-intent list must be adopted")
	}
}

// #1499 case B pin: a negated declaration is not recorded.
func Test1499NegatedDeclarationIgnored(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("This part isn't done — I'll continue with the remaining work.", 2)
	if s.declarationIter >= 0 {
		t.Fatalf("negated declaration must not be recorded (iter=%d)", s.declarationIter)
	}
	// Affirmative declaration still records.
	s2 := newSuccessDeclareState()
	s2.recordAssistantText("All tests pass and the implementation is complete.", 2)
	if s2.declarationIter < 0 {
		t.Fatal("affirmative declaration must record")
	}
}

// #1499 case C pin: continuation opener vetoes the declaration.
func Test1499ContinuationVeto(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("The fix works. Next, I'll clean up the tests.", 3)
	if s.declarationIter >= 0 {
		t.Fatal("component claim + announced continuation must not record a task-level declaration")
	}
}
