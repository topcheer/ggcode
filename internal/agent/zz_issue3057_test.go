package agent

// Regression probe for #3057: the r365 research-gate fields (searchCalls,
// fetchCalls, reportGateFired) are "this run" state living on the cross-run
// overseer instance. reset() must clear them, or the gate fired once per
// Agent LIFETIME and counters accumulated across runs (stale thresholds +
// a message lying about "calls this run").

import (
	"strings"
	"testing"
)

func TestIssue3057_ResetClearsResearchGateFields(t *testing.T) {
	o := newOverseerState()
	// Simulate a research run accumulating retrieval calls and firing.
	o.mu.Lock()
	o.searchCalls = 3
	o.fetchCalls = 2
	o.reportGateFired = true
	o.mu.Unlock()

	o.reset()

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.searchCalls != 0 || o.fetchCalls != 0 || o.reportGateFired {
		t.Fatalf("#3057: reset must clear the r365 fields, got search=%d fetch=%d fired=%v",
			o.searchCalls, o.fetchCalls, o.reportGateFired)
	}
}

func TestIssue3057_CrossRunGateCanFireAgain(t *testing.T) {
	// End-to-end shape: run 1 fires the gate; resetOverseer (run boundary)
	// must let run 2's research fire again with ITS OWN call count.
	o := newOverseerState()

	// Run 1: 4 successful retrievals, gate fires, message reports 4.
	msg1 := researchReportGate(true, 4, researchReportGateMinCalls, false)
	if msg1 == "" {
		t.Fatal("run 1 gate must fire at threshold")
	}
	if !strings.Contains(msg1, "4 successful") {
		t.Fatalf("run 1 message must report its own count: %s", msg1)
	}

	// What the old bug did: fired flag survived reset, so run 2 short-
	// circuited even with its own 4 fresh calls.
	o.reportGateFired = true // gate marked fired during run 1
	o.reset()
	o.reportGateFired = false || o.reportGateFired // read post-reset value

	// Run 2 (fresh, threshold met again): must fire again.
	o2searches := 4
	msg2 := researchReportGate(true, o2searches, researchReportGateMinCalls, o.reportGateFired)
	if msg2 == "" {
		t.Fatal("#3057: after reset, a new research run must be able to fire the gate again")
	}

	// And the stale-count lie: a run-2 counter starting at run-1's residue
	// must not happen - the probe pins the reset contract above; here we
	// assert the message reflects only the CURRENT count when it fires.
	if !strings.Contains(msg2, "4 successful") {
		t.Fatalf("run 2 message must report its own count: %s", msg2)
	}
}

func TestIssue3057_RecordToolCallAccumulatesUntilReset(t *testing.T) {
	o := newOverseerState()
	o.recordToolCall("web_search", false, "", "")
	o.recordToolCall("web_search", true, "", "") // errors don't count (#r365)
	o.recordToolCall("web_fetch", false, "", "")
	o.mu.Lock()
	sc, fc := o.searchCalls, o.fetchCalls
	o.mu.Unlock()
	if sc != 1 || fc != 1 {
		t.Fatalf("successful-only accumulation broken: search=%d fetch=%d", sc, fc)
	}
	o.reset()
	o.mu.Lock()
	sc, fc = o.searchCalls, o.fetchCalls
	o.mu.Unlock()
	if sc != 0 || fc != 0 {
		t.Fatalf("#3057: accumulation must restart per run, got search=%d fetch=%d", sc, fc)
	}
}
