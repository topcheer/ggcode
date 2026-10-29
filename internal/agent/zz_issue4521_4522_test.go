package agent

// Regression tests for GitHub issues #4521/#4522 (success_declare):
// - #4521: verification-intent caveats must veto ACROSS the sentence
//   boundary ("All done. Let me check the build." -> the declaration is
//   hedged: verification was announced as not done yet).
// - #4522: the continuation phrase "then i" must match on word
//   boundaries only - "Then in summary, ..." is NOT a continuation
//   opener and must not veto a legitimate wrap-up declaration.

import "testing"

func TestIssue4521_VerifyIntentCrossesSentence(t *testing.T) {
	// "all done" declaration ends; the NEXT sentence announces unchecked
	// verification. Windowed caveat scan must veto (it used to release the
	// veto because the caveat sat after a '.').
	const lower = "all done. let me check the build to be sure."
	if !sdHasCaveatWindowed(lower, 0, len("all done")) {
		t.Fatalf("verification intent in the next sentence must veto the declaration")
	}
	// Control: a plain hedge after the period still does NOT veto.
	const plain = "all done. however, the summary is long."
	if sdHasCaveatWindowed(plain, 0, len("all done")) {
		t.Fatalf("ordinary same-sentence-only hedge leaked across the boundary")
	}
}

func TestIssue4522_ThenIWordBoundary(t *testing.T) {
	// Declaration "the task is complete"; the following text contains
	// "then in summary" - substring "then i" must NOT count as a
	// continuation opener.
	const lower = "the task is complete. then in summary, all requirements are verified."
	declEnd := len("the task is complete")
	if sdHasContinuationWindowed(lower, declEnd) {
		t.Fatalf(`"then in" must not match the continuation phrase "then i"`)
	}
	// Control: a genuine "then i'll" continuation still vetoes.
	const cont = "the fix works. then i will clean up the tests."
	if !sdHasContinuationWindowed(cont, len("the fix works")) {
		t.Fatalf(`"then i will" is a real continuation and must still veto`)
	}
	// Edge: phrase at the very start/end of the window counts.
	if !sdPhraseAtWordBoundary("then i", "then i") {
		t.Fatalf("exact-match window must hit")
	}
	if sdPhraseAtWordBoundary("then insurance", "then i") {
		t.Fatalf("prefix-of-longer-word must not hit")
	}
}
