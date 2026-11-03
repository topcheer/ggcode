package agent

import "testing"

func TestIssue4129NeutralRestatementNotFrustration(t *testing.T) {
	neutral := []string{

		"as I said, the parser retries on 429 errors",
		"like I said earlier, use the mutex-free variant",
		"I said the handler already covers that case",
	}
	for _, msg := range neutral {
		if got := detectNegativeFeedback(msg); got == negCatFrustration {
			t.Errorf("neutral restatement read as frustration: %q -> %q", msg, got)
		}
	}
}

func TestIssue4129ReprimandStillFires(t *testing.T) {
	reprimand := []string{

		"I already told you twice: do not touch internal/auth",

		"我说过了别动 config.yaml",
	}
	for _, msg := range reprimand {
		if got := detectNegativeFeedback(msg); got != negCatFrustration {
			t.Errorf("reprimand not detected: %q -> %q", msg, got)
		}
	}
}
