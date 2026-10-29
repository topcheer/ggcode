package agent

// Regression tests for GitHub issue #4539 (success_declare): the
// leading-edge branches (negation lookbehind window and the leading
// caveat branch) lacked the sentence-boundary check the trailing branch
// has always had, so cross-sentence negations/hedges killed legitimate
// declarations:
//   A: "We fixed the bug that was not working. The task is done."
//   B: "However, see caveats below. The task is complete." (documented
//      wrap-up shape, L79-83 contract).

import "testing"

func TestIssue4539_NegationCrossesSentence(t *testing.T) {
	// Cross-sentence negation must NOT veto; the declaration index points
	// at the phrase start ("the task is done").
	const lowerA = "we fixed the bug that was not working. the task is done."
	declA := len("we fixed the bug that was not working. ")
	if sdHasNegationBefore(lowerA, declA) {
		t.Fatalf("cross-sentence 'not working' must not veto the declaration")
	}
	const lowerB = "not yet benchmarked. all done."
	declB := len("not yet benchmarked. ")
	if sdHasNegationBefore(lowerB, declB) {
		t.Fatalf("cross-sentence 'not yet' must not veto the declaration")
	}
	// Control: same-sentence negation still vetoes.
	const same = "this is not working, the task is done"
	if !sdHasNegationBefore(same, len("this is not working, ")) {
		t.Fatalf("same-sentence negation must still veto")
	}
}

func TestIssue4539_LeadingCaveatCrossesSentence(t *testing.T) {
	// "however" in its own previous sentence must not veto the claim.
	const lower = "however, see caveats below. the task is complete."
	decl := len("however, see caveats below. ")
	if sdHasCaveatWindowed(lower, decl, decl+len("the task is complete")) {
		t.Fatalf("cross-sentence leading hedge must not veto the declaration")
	}
	// Control: same-sentence leading hedge still vetoes.
	const same = "however, the task is complete"
	if !sdHasCaveatWindowed(same, len("however, "), len(same)) {
		t.Fatalf("same-sentence leading hedge must still veto")
	}
}
