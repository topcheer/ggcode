package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
)

// #669 defect 1: LastTurnIndex must be a full max scan, not last-element
// only. #656/#657 tolerate dropping corrupted trailing records, so the final
// element can carry a lower turn index than earlier records — trusting only
// the last element under-reported the AdoptSession turn baseline and caused
// turn numbers to be reused (metrics merge, UsageTurnIndex double count).
func TestIssue669LastTurnIndexFullMaxScan(t *testing.T) {
	if got := LastTurnIndex(nil); got != 0 {
		t.Fatalf("nil session → 0, got %d", got)
	}

	ses := &Session{
		UsageHistory: []UsageEntry{
			{TurnIndex: 1},
			{TurnIndex: 2},
			{TurnIndex: 7}, // max, NOT last
			{TurnIndex: 3}, // trailing record (e.g. after a corrupted drop)
		},
	}
	if got := LastTurnIndex(ses); got != 7 {
		t.Fatalf("UsageHistory max = %d, want 7", got)
	}

	// Metrics max must also be considered, even when UsageHistory is empty.
	ses2 := &Session{
		Metrics: []metrics.MetricEvent{
			{TurnIndex: 2},
			{TurnIndex: 11},
			{TurnIndex: 4},
		},
	}
	if got := LastTurnIndex(ses2); got != 11 {
		t.Fatalf("Metrics max = %d, want 11", got)
	}

	// Cross-source max: metrics higher than usage history.
	ses3 := &Session{
		UsageHistory: []UsageEntry{{TurnIndex: 5}},
		Metrics:      []metrics.MetricEvent{{TurnIndex: 8}},
	}
	if got := LastTurnIndex(ses3); got != 8 {
		t.Fatalf("cross-source max = %d, want 8", got)
	}

	// Empty session → 0.
	if got := LastTurnIndex(&Session{}); got != 0 {
		t.Fatalf("empty session → 0, got %d", got)
	}
}
