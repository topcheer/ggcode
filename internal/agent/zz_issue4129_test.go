package agent

// zz_issue4129_test.go pins the #4129 fix: the bare restatement markers
// "i said" / "as i said" / "like i said" / "i already told" in the
// frustration pattern list fired on neutral technical restatement
// ("as I said, the parser retries on 429") - two such messages reset
// monitoring. The bare forms are removed; only the agent-directed
// reprimand form "i already told you" keeps firing, and the CJK
// counterpart 我说过了 (already reprimand-shaped) is untouched.

import "testing"

func TestIssue4129NeutralRestatementNotFrustration(t *testing.T) {
	neutral := []string{
		// Restating prior technical context is not frustration.
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
		// Directed at the agent: restatement + "you" is a scolding shape.
		"I already told you twice: do not touch internal/auth",
		// CJK reprimand form must be untouched by this fix.
		"我说过了别动 config.yaml",
	}
	for _, msg := range reprimand {
		if got := detectNegativeFeedback(msg); got != negCatFrustration {
			t.Errorf("reprimand not detected: %q -> %q", msg, got)
		}
	}
}
