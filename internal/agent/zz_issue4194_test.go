package agent

import "testing"

func TestIssue4194_CapitalizedQuestionFormsDetected(t *testing.T) {
	questions := []string{
		"Why is this still failing",
		"Is it wrong",
		"How is that broken",
		"why is this still failing",
		"Why is this still failing?",
		"为什么 still broken？",
	}
	for _, q := range questions {
		if !isEnglishQuestionLine(q) {
			t.Errorf("isEnglishQuestionLine(%q) = false, want true", q)
		}
	}
}

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

func TestIssue4194_EndToEndNoFalseRejection(t *testing.T) {
	if got := detectNegativeFeedback("Why is this still failing"); got != "" {
		t.Fatalf("capitalized question leaked through gate: detectNegativeFeedback = %q", got)
	}
}
