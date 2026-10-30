package agent

import "testing"

// #4637: negation phrases that END with the declared word ("isn't done",
// "not finished", ...) could never match - the lookbehind window stopped at
// declIdx, before the declared word, making 13 of 14 negation patterns dead
// code. A truthful "this part isn't done - I'll continue" was recorded as a
// success declaration and then flagged as premature success.
func TestIssue4637_NegationEndingInDeclaredWordVetoes(t *testing.T) {
	const lower = "this part isn't done - i'll continue"
	declIdx := len("this part isn't")
	// Declaration phrase is "done" starting right after "isn't ".
	// Simulate the real call shape: decl phrase "done" at that index.
	if !sdHasNegationBefore(lower, len("this part isn't "), len("this part isn't done")) {
		t.Fatal("negation ending in the declared word must veto the declaration (#4637)")
	}
	_ = declIdx
}

// Control: a plain declaration with no negation still passes.
func TestIssue4637_PlainDeclarationNotVetoed(t *testing.T) {
	const lower = "the build passes and the task is done"
	decl := len("the build passes and the task is ")
	if sdHasNegationBefore(lower, decl, decl+len("done")) {
		t.Fatal("plain declaration must not be vetoed (#4637)")
	}
}

// Control preserved from #4539 semantics: cross-sentence negation still
// does not veto even with the extended window.
func TestIssue4637_CrossSentenceStillAllowed(t *testing.T) {
	const lower = "we fixed the bug that was not working. the task is done"
	decl := len("we fixed the bug that was not working. ")
	if sdHasNegationBefore(lower, decl, decl+len("done")) {
		t.Fatal("cross-sentence negation must not veto (#4539 contract)")
	}
}
