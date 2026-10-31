package agent

import (
	"os"
	"strings"
	"testing"
)

// #4650: no-tool-path detector guidance (truncClaim, circularReasoning,
// contradiction, scopeCreep, ... goalDrift) was injected without a
// continue - on the clean-exit path (no later gate firing) the message
// never reached the LLM in-run and resurfaced as stale context next turn
// (#3850/#367 dead-letter precedent). The detector block must be followed
// by a bounded continue that gives the guidance a turn to be consumed.
func TestIssue4650_DetectorGuidanceContinuesRun(t *testing.T) {
	data, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "detectorMsgsBefore := len(a.contextManager.Messages())") {
		t.Fatal("detector block must snapshot context length before the sites (#4650)")
	}
	if !strings.Contains(text, "len(a.contextManager.Messages()) > detectorMsgsBefore && detectorContinueBudget > 0") {
		t.Fatal("post-block continue must fire when guidance was injected and budget remains (#4650)")
	}
	if !strings.Contains(text, "detectorContinueBudget := 2") {
		t.Fatal("per-run continue budget must bound the loop (#4650)")
	}
	// The continue must sit BEFORE the no-tool-path interruption gate (the
	// gate call appears twice - tool path first; the no-tool occurrence is
	// the last one, after the detector block).
	contIdx := strings.Index(text, "detector guidance injected; continuing run to consume it")
	gateIdx := strings.LastIndex(text, "a.injectPendingInterruptions(i + 1); injected {")
	if contIdx < 0 || gateIdx < 0 || contIdx > gateIdx {
		t.Fatal("detector continue must precede the interruption gate (#4650)")
	}
}
