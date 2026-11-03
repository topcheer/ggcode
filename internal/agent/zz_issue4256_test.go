package agent

import "testing"

func TestIssue4256_WorkToBeDoneNotADeclaration(t *testing.T) {
	sd := newSuccessDeclareState()
	sd.recordAssistantText("There is still work to be done: fix the parser bug and update the docs.", 3)
	if sd.declarationIter > 0 {
		t.Fatalf("passive-negation sentence recorded as completion at iter %d - 180-degree inversion", sd.declarationIter)
	}
}

func TestIssue4256_VerifyIntentNotADeclaration(t *testing.T) {
	sd := newSuccessDeclareState()
	sd.recordAssistantText("Let me verify if the fix works", 5)
	if sd.declarationIter > 0 {
		t.Fatalf("verify-intent sentence recorded as completion at iter %d", sd.declarationIter)
	}
}
