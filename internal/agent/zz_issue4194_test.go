package agent

import "testing"

// #4194: IM auto-capitalization + dropped question mark ("Why is this
// still failing") defeated the #4190 question gate: the function compared
// the raw line against an all-lowercase prefix list while the rejection
// scanner lowercased its own copy. Full-width ？ endings were also
// invisible to the ASCII suffix check.
func TestIssue4194_CapitalizedQuestionFormsDetected(t *testing.T) {
	questions := []string{
		"Why is this still failing",
		"Is it wrong",
		"How is that broken",
		"why is this still failing",  // original lowercase form still works
		"Why is this still failing?", // explicit question mark still works
		"为什么 still broken？",          // full-width question mark
	}
	for _, q := range questions {
		if !isEnglishQuestionLine(q) {
			t.Errorf("isEnglishQuestionLine(%q) = false, want true", q)
		}
	}
}

// Control: capitalized non-question statements stay undetected - the gate
// must protect questions, not blanket-mask capitalized lines.
func TestIssue4194_CapitalizedStatementsNotDetected(t *testing.T) {
	statements := []string{
		"Still broken",
		"This is wrong",
	}
	for _, s := range statements {
		if isEnglishQuestionLine(s) {
			t.Errorf("isEnglishQuestionLine(%q) = true, want false", s)
		}
	}
}

// End-to-end: the issue's trigger sentence must no longer classify as
// rejection (short line, direct category path).
func TestIssue4194_EndToEndNoFalseRejection(t *testing.T) {
	if got := detectNegativeFeedback("Why is this still failing"); got != "" {
		t.Fatalf("capitalized question leaked through gate: detectNegativeFeedback = %q", got)
	}
}
