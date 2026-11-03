package agent

import "testing"

func TestIssue4521_VerifyIntentCrossesSentence(t *testing.T) {
	const lower = "all done. let me check the build to be sure."
	if !sdHasCaveatWindowed(lower, 0, len("all done")) {
		t.Fatalf("verification intent in the next sentence must veto the declaration")
	}
	const plain = "all done. however, the summary is long."
	if sdHasCaveatWindowed(plain, 0, len("all done")) {
		t.Fatalf("ordinary same-sentence-only hedge leaked across the boundary")
	}
}

func TestIssue4522_ThenIWordBoundary(t *testing.T) {
	const lower = "the task is complete. then in summary, all requirements are verified."
	declEnd := len("the task is complete")
	if sdHasContinuationWindowed(lower, declEnd) {
		t.Fatalf(`"then in" must not match the continuation phrase "then i"`)
	}
	const cont = "the fix works. then i will clean up the tests."
	if !sdHasContinuationWindowed(cont, len("the fix works")) {
		t.Fatalf(`"then i will" is a real continuation and must still veto`)
	}

	if !sdPhraseAtWordBoundary("then i", "then i") {
		t.Fatalf("exact-match window must hit")
	}
	if sdPhraseAtWordBoundary("then insurance", "then i") {
		t.Fatalf("prefix-of-longer-word must not hit")
	}
}
