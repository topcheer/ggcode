package im

// Regression probe for #3091: daemonSessionTurnIndex must read the shared
// UsageHistory/Metrics slices through the #3086/#3087 lock-guarded
// snapshot accessors instead of bare slice indexing.

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

	// TurnIndex from the last usage entry wins over an older metric.
	ses.AddUsageHistoryEntry(session.UsageEntry{TurnIndex: 3})
	ses.AppendMetricEvent(metrics.MetricEvent{TurnIndex: 1})
	if got := daemonSessionTurnIndex(ses); got != 3 {
		t.Fatalf("usage-max: got %d, want 3", got)
	}

	// A newer metric entry wins over the older usage entry.
	ses.AppendMetricEvent(metrics.MetricEvent{TurnIndex: 7})
	if got := daemonSessionTurnIndex(ses); got != 7 {
		t.Fatalf("metric-max: got %d, want 7", got)
	}
}
