package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
)

func TestIssue669LastTurnIndexFullMaxScan(t *testing.T) {
	if got := LastTurnIndex(nil); got != 0 {
		t.Fatalf("nil session → 0, got %d", got)
	}

	ses := &Session{
		UsageHistory: []UsageEntry{
			{TurnIndex: 1},
			{TurnIndex: 2},
			{TurnIndex: 7},
			{TurnIndex: 3},
		},
	}
	if got := LastTurnIndex(ses); got != 7 {
		t.Fatalf("UsageHistory max = %d, want 7", got)
	}

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

	ses3 := &Session{
		UsageHistory: []UsageEntry{{TurnIndex: 5}},
		Metrics:      []metrics.MetricEvent{{TurnIndex: 8}},
	}
	if got := LastTurnIndex(ses3); got != 8 {
		t.Fatalf("cross-source max = %d, want 8", got)
	}

	if got := LastTurnIndex(&Session{}); got != 0 {
		t.Fatalf("empty session → 0, got %d", got)
	}
}
