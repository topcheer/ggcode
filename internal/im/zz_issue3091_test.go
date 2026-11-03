package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/session"
)

func TestIssue3091_TurnIndexUsesSnapshots(t *testing.T) {
	if got := daemonSessionTurnIndex(nil); got != 0 {
		t.Fatalf("nil session: got %d, want 0", got)
	}
	ses := &session.Session{}
	if got := daemonSessionTurnIndex(ses); got != 0 {
		t.Fatalf("empty session: got %d, want 0", got)
	}

	ses.AddUsageHistoryEntry(session.UsageEntry{TurnIndex: 3})
	ses.AppendMetricEvent(metrics.MetricEvent{TurnIndex: 1})
	if got := daemonSessionTurnIndex(ses); got != 3 {
		t.Fatalf("usage-max: got %d, want 3", got)
	}

	ses.AppendMetricEvent(metrics.MetricEvent{TurnIndex: 7})
	if got := daemonSessionTurnIndex(ses); got != 7 {
		t.Fatalf("metric-max: got %d, want 7", got)
	}
}
