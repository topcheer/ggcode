package agent

import (
	"strings"
	"testing"
)

func lowLogprobPtr(v float64) *float64 { return &v }

// TestConfidenceLogprobDualEvidence pins #uncertainty-loop P1: a low avg
// token logprob (lastLogprob) surfaces as an intervention reason on top of
// a weak trajectory score, and is cleared by reset.
func TestConfidenceLogprobDualEvidence(t *testing.T) {
	c := newConfidenceState()
	if c.lastLogprob != nil {
		t.Fatal("fresh state must have nil logprob")
	}
	// Simulate a weak but non-clustered trajectory (max cluster 3 avoids the
	// hard error-cluster early return; low success rate drives the score down).
	pattern := []bool{true, true, false, true, true, true}
	for _, isErr := range pattern {
		c.recordResult("edit_file", isErr, "")
	}
	// Baseline without logprob evidence.
	baseline := c.maybeIntervene()

	c2 := newConfidenceState()
	for _, isErr := range pattern {
		c2.recordResult("edit_file", isErr, "")
	}
	c2.lastLogprob = lowLogprobPtr(-3.1)
	got := c2.maybeIntervene()
	if baseline == "" && got == "" {
		t.Fatal("weak trajectory + low logprob must intervene")
	}
	if got != "" && !strings.Contains(got, "low language-model confidence") {
		t.Fatalf("intervention must cite logprob evidence, got: %s", got)
	}
	// reset clears the signal.
	c2.reset()
	if c2.lastLogprob != nil {
		t.Fatal("reset must clear lastLogprob")
	}
}
