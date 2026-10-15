package agent

import "testing"

// #4190: the question gate (#4149) only guarded CJK patterns, so English
// diagnostic questions embedding rejection substrings ("still broken",
// "keep failing", "not working") were misclassified as rejection feedback
// and could reset the five monitoring subsystems via correction_feedback.
func TestEnglishQuestionGateIssue4190(t *testing.T) {
	// English diagnostic questions: must NOT be rejection.
	questions := []string{
		"is the build still broken?",
		"why does the auth flow keep failing?",
		"it's not working?",
		"is the test still failing?",
		"does the deploy still fail?",
		"what part is broken?",
		"how come it keeps failing?",
	}
	for _, msg := range questions {
		if c := detectNegativeFeedback(msg); c != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (diagnostic question)", msg, c)
		}
	}

	// CJK control: the #4149 gate keeps working.
	if c := detectNegativeFeedback("部署没成功吗？"); c != "" {
		t.Errorf("detectNegativeFeedback(CJK question) = %q, want empty", c)
	}

	// Positive controls: genuine statements still detected.
	if c := detectNegativeFeedback("the build is still broken"); c != "rejection" {
		t.Errorf("detectNegativeFeedback(genuine broken) = %q, want rejection", c)
	}
	if c := detectNegativeFeedback("it's not working"); c != "rejection" {
		t.Errorf("detectNegativeFeedback(genuine not working) = %q, want rejection", c)
	}
	if c := detectNegativeFeedback("还是错了，重新做"); c != "rejection" {
		t.Errorf("detectNegativeFeedback(CJK genuine) = %q, want rejection", c)
	}
}

// The #4190 gate is scoped to the rejection category: a question-form
// message matching a redirection pattern keeps its redirection signal
// (pre-existing behavior, explicitly unchanged by #4190).
func TestEnglishQuestionGateScopeIssue4190(t *testing.T) {
	if c := detectNegativeFeedback("can we switch to plan instead of that?"); c != "redirection" {
		t.Errorf("detectNegativeFeedback(question-form redirection) = %q, want redirection (gate is rejection-scoped)", c)
	}
}
