package agent

import "testing"

// #4256 gap 1: "still work to be done: ..." must NOT record a completion
// declaration (passive negation form of "isn't done").
func TestIssue4256_WorkToBeDoneNotADeclaration(t *testing.T) {
	sd := newSuccessDeclareState()
	sd.recordAssistantText("There is still work to be done: fix the parser bug and update the docs.", 3)
	if sd.declarationIter > 0 {
		t.Fatalf("passive-negation sentence recorded as completion at iter %d - 180-degree inversion", sd.declarationIter)
	}
}

// #4256 gap 2: verify-intent framing must not hit the "works" declaration.
func TestIssue4256_VerifyIntentNotADeclaration(t *testing.T) {
	sd := newSuccessDeclareState()
	sd.recordAssistantText("Let me verify if the fix works", 5)
	if sd.declarationIter > 0 {
		t.Fatalf("verify-intent sentence recorded as completion at iter %d", sd.declarationIter)
	}
}
