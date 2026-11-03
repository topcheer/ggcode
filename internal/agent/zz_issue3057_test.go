package agent

import (
	"strings"
	"testing"
)

func TestIssue3057_ResetClearsResearchGateFields(t *testing.T) {
	o := newOverseerState()

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

	o := newOverseerState()

	msg1 := researchReportGate(true, 4, researchReportGateMinCalls, false)
	if msg1 == "" {
		t.Fatal("run 1 gate must fire at threshold")
	}
	if !strings.Contains(msg1, "4 successful") {
		t.Fatalf("run 1 message must report its own count: %s", msg1)
	}

	o.reportGateFired = true
	o.reset()
	o.reportGateFired = false || o.reportGateFired

	o2searches := 4
	msg2 := researchReportGate(true, o2searches, researchReportGateMinCalls, o.reportGateFired)
	if msg2 == "" {
		t.Fatal("#3057: after reset, a new research run must be able to fire the gate again")
	}

	if !strings.Contains(msg2, "4 successful") {
		t.Fatalf("run 2 message must report its own count: %s", msg2)
	}
}

func TestIssue3057_RecordToolCallAccumulatesUntilReset(t *testing.T) {
	o := newOverseerState()
	o.recordToolCall("web_search", false, "", "", 999)
	o.recordToolCall("web_search", true, "", "", 999)
	o.recordToolCall("web_fetch", false, "", "", 999)
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
