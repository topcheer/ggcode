package agent

import "testing"

func TestIssue4539_NegationCrossesSentence(t *testing.T) {
	const lowerA = "we fixed the bug that was not working. the task is done."
	declA := len("we fixed the bug that was not working. ")
	if sdHasNegationBefore(lowerA, declA, declA+len("the task is done")) {
		t.Fatalf("cross-sentence 'not working' must not veto the declaration")
	}
	const lowerB = "not yet benchmarked. all done."
	declB := len("not yet benchmarked. ")
	if sdHasNegationBefore(lowerB, declB, declB+len("all done")) {
		t.Fatalf("cross-sentence 'not yet' must not veto the declaration")
	}
	const same = "this is not working, the task is done"
	if !sdHasNegationBefore(same, len("this is not working, "), len(same)) {
		t.Fatalf("same-sentence negation must still veto")
	}
}

func TestIssue4539_LeadingCaveatCrossesSentence(t *testing.T) {
	const lower = "however, see caveats below. the task is complete."
	decl := len("however, see caveats below. ")
	if sdHasCaveatWindowed(lower, decl, decl+len("the task is complete")) {
		t.Fatalf("cross-sentence leading hedge must not veto the declaration")
	}
	const same = "however, the task is complete"
	if !sdHasCaveatWindowed(same, len("however, "), len(same)) {
		t.Fatalf("same-sentence leading hedge must still veto")
	}
}
