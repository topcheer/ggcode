package agent

import (
	"strings"
	"testing"
)

// #5199-user-report: the continuation advisory fired at 60% of the RAW
// window (UsageRatio) while actual auto-compaction fires around 90%
// (window minus output reserve) - the LLM saw "context usage is high /
// nearly exhausted" hints ~30 points before the real compaction point.
// Aligned: occupancy is now tokens vs the AutoCompactThreshold, and the
// advisory talks about compaction headroom, not raw window.

func TestIssue5204_AdvisoryTalksCompactionNotRawWindow(t *testing.T) {
	msg := ptGuardAdvisory(6, 10, 0.9)
	if !strings.Contains(msg, "compaction threshold") {
		t.Errorf("advisory must be compaction-relative, got: %s", msg)
	}
	if !strings.Contains(msg, "headroom before auto-compaction") {
		t.Errorf("advisory must reference compaction headroom, got: %s", msg)
	}
	if strings.Contains(msg, "context window is still available") {
		t.Errorf("advisory must not use the raw-window framing: %s", msg)
	}
}

func TestIssue5204_ThresholdIsCompactionRelative(t *testing.T) {
	// 0.85 of the compaction point ~= 76% of the raw window at the default
	// 10% output reserve - close to compaction, not 30 points early.
	if ptGuardOccupancy != 0.85 {
		t.Fatalf("ptGuardOccupancy = %v, want 0.85 (compaction-relative)", ptGuardOccupancy)
	}
	msg := ptGuardAdvisory(6, 10, ptGuardOccupancy)
	if !strings.Contains(msg, "85% of the compaction threshold") {
		t.Errorf("advisory occupancy must render on the compaction scale: %s", msg)
	}
}

func TestIssue5204_HeadroomMath(t *testing.T) {
	msg := ptGuardAdvisory(6, 10, 0.85)
	if !strings.Contains(msg, "15% of the headroom") {
		t.Errorf("headroom percent must be compaction-relative (1-0.85), got: %s", msg)
	}
}
