package agent

import "testing"

func TestIssue4203_PluralNoVariantsNotNegative(t *testing.T) {
	cases := []string{
		"No problems, looks good to me",
		"no problems",
		"No sweat, go ahead",
		"no complaints here",
		"No concerns from my side",
	}
	for _, msg := range cases {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", msg, got)
		}
	}
}

func TestIssue4203_RealRejectionStillDetected(t *testing.T) {
	cases := []string{
		"No, redo it from scratch",
		"no problems were found, but this approach is still wrong",
	}
	for _, msg := range cases {
		if got := detectNegativeFeedback(msg); got == "" {
			t.Errorf("detectNegativeFeedback(%q) = empty, want rejection", msg)
		}
	}
}
