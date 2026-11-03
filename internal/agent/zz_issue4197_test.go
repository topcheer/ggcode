package agent

import "testing"

func TestIssue4197_NoNeedNotNegative(t *testing.T) {
	cases := []string{
		"No need to rerun tests, thanks!",
		"no need to rerun tests",
		"No need to wait",
		"no need to apologize",
	}
	for _, msg := range cases {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty", msg, got)
		}
	}
}

func TestIssue4197_RealRejectionStillDetected(t *testing.T) {
	cases := []string{
		"No, this is wrong",
		"no, stop doing it that way",
	}
	for _, msg := range cases {
		if got := detectNegativeFeedback(msg); got == "" {
			t.Errorf("detectNegativeFeedback(%q) = empty, want rejection", msg)
		}
	}
}
