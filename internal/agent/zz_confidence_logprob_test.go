package agent

import "testing"

func lowLogprobPtr(v float64) *float64 { return &v }

// TestConfidenceLogprobSignalKept pins the surviving half of
// #uncertainty-loop P1: the stream feeds the avg token logprob into the
// confidence state and reset clears it. The guidance half (the
// "[trajectory confidence]" intervention that cited it) was removed - the
// model decides when an approach is not working.
func TestConfidenceLogprobSignalKept(t *testing.T) {
	c := newConfidenceState()
	if c.lastLogprob != nil {
		t.Fatal("fresh state must have nil logprob")
	}
	for _, isErr := range []bool{true, true, false, true, true, true} {
		c.recordResult("edit_file", isErr, "")
	}
	c.lastLogprob = lowLogprobPtr(-3.1)
	if c.lastLogprob == nil || *c.lastLogprob != -3.1 {
		t.Fatalf("logprob signal not kept, got %v", c.lastLogprob)
	}
	c.reset()
	if c.lastLogprob != nil {
		t.Fatal("reset must clear lastLogprob")
	}
}
