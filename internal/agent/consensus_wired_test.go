package agent

import (
	"strings"
	"testing"
)

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
