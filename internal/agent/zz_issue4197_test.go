package agent

import "testing"

// #4197: "no need to X" is a neutral/benign instruction (same family as
// "no problem" / "no worries") but was missing from the positive whitelist.
// The bare rejection word "no" at the start of a short message then took the
// isShort rejection path and injected misleading "do NOT repeat your
// previous approach" guidance for a friendly instruction.
func TestIssue4197_NoNeedNotNegative(t *testing.T) {
	cases := []string{
		"No need to rerun tests, thanks!", // exact trigger from the issue
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

// Control: genuine rejections must still fire after the whitelist addition.
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
