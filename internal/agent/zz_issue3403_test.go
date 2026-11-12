package agent

import (
	"strings"
	"testing"
)

func Test3403SpiralStateResetWiredIntoRunStreamBatch(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "a.spiralState.reset()") {
		t.Fatal("#3403: spiralState.reset() is not wired into the RunStream per-run reset batch - the detector violates its per-run contract (dead reset() caused quota leakage + stale cross-run topics)")
	}

	for _, ln := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(ln, " \t")
		if strings.HasPrefix(trimmed, "a.spiralState.reset()") {
			return
		}
	}
	t.Fatal("#3403: a.spiralState.reset() appears only inside a comment - wire the live call into the RunStream reset batch")
}
