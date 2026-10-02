package agent

import (
	"strings"
	"testing"
)

func TestResearchReportGate(t *testing.T) {
	fired := researchReportGate(true, 5, researchReportGateMinCalls, false)
	if !strings.Contains(fired, "[Research Synthesis Gate]") || !strings.Contains(fired, "conflicts") || !strings.Contains(fired, "5 successful") {
		t.Fatalf("gate message wrong: %q", fired)
	}
	if researchReportGate(true, 3, researchReportGateMinCalls, false) != "" {
		t.Error("below threshold must pass")
	}
	if researchReportGate(false, 9, researchReportGateMinCalls, false) != "" {
		t.Error("non-research mode must pass")
	}
	if researchReportGate(true, 9, researchReportGateMinCalls, true) != "" {
		t.Error("fire-once: already fired must pass")
	}
	if researchReportGate(true, 4, researchReportGateMinCalls, false) == "" {
		t.Error("exactly at threshold must fire")
	}
}

// TestOverseerRetrievalCounters pins the r365 recordToolCall counting:
// successful searches/fetches accumulate; errors (retries against the same
// source) must not inflate the multi-hop signal; other tools stay at zero.
func TestOverseerRetrievalCounters(t *testing.T) {
	o := newOverseerState()
	o.recordToolCall("web_search", false, "")
	o.recordToolCall("web_search", true, "") // failed retry: not counted
	o.recordToolCall("web_fetch", false, "")
	o.recordToolCall("read_file", false, "/x.go")
	if o.searchCalls != 1 || o.fetchCalls != 1 {
		t.Fatalf("counters: search=%d fetch=%d, want 1/1 (errors excluded)", o.searchCalls, o.fetchCalls)
	}
}
