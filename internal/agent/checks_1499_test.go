package agent

import (
	"testing"
)

func Test1499NegatedDeclarationIgnored(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("This part isn't done — I'll continue with the remaining work.", 2)
	if s.declarationIter >= 0 {
		t.Fatalf("negated declaration must not be recorded (iter=%d)", s.declarationIter)
	}

	s2 := newSuccessDeclareState()
	s2.recordAssistantText("All tests pass and the implementation is complete.", 2)
	if s2.declarationIter < 0 {
		t.Fatal("affirmative declaration must record")
	}
}

func Test1499ContinuationVeto(t *testing.T) {
	s := newSuccessDeclareState()
	s.recordAssistantText("The fix works. Next, I'll clean up the tests.", 3)
	if s.declarationIter >= 0 {
		t.Fatal("component claim + announced continuation must not record a task-level declaration")
	}
}
