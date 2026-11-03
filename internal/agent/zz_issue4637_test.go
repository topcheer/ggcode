package agent

import "testing"

func TestIssue4637_NegationEndingInDeclaredWordVetoes(t *testing.T) {
	const lower = "this part isn't done - i'll continue"
	declIdx := len("this part isn't")

	if !sdHasNegationBefore(lower, len("this part isn't "), len("this part isn't done")) {
		t.Fatal("negation ending in the declared word must veto the declaration (#4637)")
	}
	_ = declIdx
}

func TestIssue4637_PlainDeclarationNotVetoed(t *testing.T) {
	const lower = "the build passes and the task is done"
	decl := len("the build passes and the task is ")
	if sdHasNegationBefore(lower, decl, decl+len("done")) {
		t.Fatal("plain declaration must not be vetoed (#4637)")
	}
}

func TestIssue4637_CrossSentenceStillAllowed(t *testing.T) {
	const lower = "we fixed the bug that was not working. the task is done"
	decl := len("we fixed the bug that was not working. ")
	if sdHasNegationBefore(lower, decl, decl+len("done")) {
		t.Fatal("cross-sentence negation must not veto (#4539 contract)")
	}
}
