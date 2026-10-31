package agent

import "testing"

// #4203: plural/collective "no X" confirmations ("no problems", "no sweat",
// "no complaints", "no concerns") were missing from the positive whitelist
// while singular "no problem" and plural "no issues" were present. The bare
// "no" survived masking and the isShort rejection path classified
// "No problems, looks good to me" as rejection, injecting course-correction
// guidance and escalating the monitoring feedback counter.
func TestIssue4203_PluralNoVariantsNotNegative(t *testing.T) {
	cases := []string{
		"No problems, looks good to me", // exact trigger from the issue
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

// Control: genuine rejections with the same leading "No" must still fire —
// the whitelist additions mask the carrier phrase, not the rejection scan.
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
