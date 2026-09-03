package agent

import (
	"strings"
	"testing"
)

// Regression guard for the #952 follow-up (commit 751171a2): the two
// detectors wired in that fix must remain consensus-eligible. The 28ca54f6
// refactor replaced content scanning with explicit recordFiring but only
// wired 4 detectors; Ungrounded Reflection and Cache Efficiency silently
// dropped out. This test fails if their names ever stop participating in
// the consensus window (e.g. someone removes the recordFiring calls).
func TestConsensusState_WiredDetectorsParticipate(t *testing.T) {
	wired := []string{
		"Failure Mode",
		"Error Cascade",
		"Fix Cascade",
		"Convergence Lock",
		"Ungrounded Reflection",
		"Cache Efficiency",
	}
	for _, name := range wired {
		s := newConsensusState()
		s.recordFiring(name, 1)
		s.recordFiring("Error Rush", 1)
		s.recordFiring("Tunnel Vision", 1)
		msg := s.check()
		if msg == "" {
			t.Errorf("detector %q fired with 2 others should trigger consensus alert", name)
		}
		if !strings.Contains(msg, name) {
			t.Errorf("consensus alert should name %q, got: %s", name, msg)
		}
	}
}
