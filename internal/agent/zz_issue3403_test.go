package agent

import (
	"os"
	"strings"
	"testing"
)

// #3403 source pin: spiral_hallucin.go states its own lifecycle contract
// ("fires at most once per run / resets on new user turn") but reset() was
// implemented as dead code and never wired into the RunStream per-run reset
// batch - the warnings=1 quota and topic registry accumulated for the whole
// Agent lifetime (long-lived TUI/desktop/IM agents), causing cross-run stale
// topic false positives and permanently muting genuine spirals after one
// stale firing. Mirrors the #1826 quota-registry pin: the mechanical guard
// fails the test stage if the wiring goes missing again.
func Test3403SpiralStateResetWiredIntoRunStreamBatch(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "a.spiralState.reset()") {
		t.Fatal("#3403: spiralState.reset() is not wired into the RunStream per-run reset batch - the detector violates its per-run contract (dead reset() caused quota leakage + stale cross-run topics)")
	}
	// Guard against a commented-out pin passing: the live call must be
	// uncommented Go (strip block/line comments crudely by requiring the
	// call to start at line-begin indentation).
	for _, ln := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(ln, " \t")
		if strings.HasPrefix(trimmed, "a.spiralState.reset()") {
			return // live wiring found
		}
	}
	t.Fatal("#3403: a.spiralState.reset() appears only inside a comment - wire the live call into the RunStream reset batch")
}
