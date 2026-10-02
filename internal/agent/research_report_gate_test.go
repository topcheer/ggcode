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
