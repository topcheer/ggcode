package agent

import "testing"

// #4121: bare "make it" must not classify an ordinary refinement
// instruction as a goal Revision - the Revision preamble makes the model
// abandon valid in-flight work. Explicit redirection markers stay detected.
func TestIssue4121_MakeItIsNotRevisionMarker(t *testing.T) {
	refinements := []string{
		"make it more robust before merging",
		"make it configurable via the flags struct, not env vars",
		"please make it faster",
	}
	for _, msg := range refinements {
		if got := classifyInterruption(msg); got != InterruptionAddition {
			t.Errorf("classifyInterruption(%q) = %v, want Addition (refinement, not revision)", msg, got)
		}
	}
}

func TestIssue4121_GenuineRevisionMarkersStillFire(t *testing.T) {
	genuine := []string{
		"change it to use a queue instead",
		"new direction: focus on the API first",
		"instead, use the buffered writer",
		"revise the goal to cover the mobile app too",
	}
	for _, msg := range genuine {
		if got := classifyInterruption(msg); got != InterruptionRevision {
			t.Errorf("classifyInterruption(%q) = %v, want Revision", msg, got)
		}
	}
}
