package agent

import "testing"

func TestEnglishQuestionGateIssue4190(t *testing.T) {

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

	if c := detectNegativeFeedback("部署没成功吗？"); c != "" {
		t.Errorf("detectNegativeFeedback(CJK question) = %q, want empty", c)
	}

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

func TestEnglishQuestionGateScopeIssue4190(t *testing.T) {
	if c := detectNegativeFeedback("can we switch to plan instead of that?"); c != "redirection" {
		t.Errorf("detectNegativeFeedback(question-form redirection) = %q, want redirection (gate is rejection-scoped)", c)
	}
}
