package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #5700: a child entering idle delivers exactly one report; consecutive
// terminal transitions (double-Complete, Complete-after-Cancel) must never
// double-enqueue, and the dequeue side is consume-once.
func TestIssue5700DoubleReportSingleDequeue(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	id := m.Spawn("worker", "task", "task", nil, context.Background())

	m.Complete(id, "", nil)
	m.Complete(id, "late result", nil)
	m.Cancel(id)

	reports := m.DrainIdleReports()
	if len(reports) != 1 {
		t.Fatalf("want exactly 1 idle report after double-Complete + Cancel, got %d", len(reports))
	}
	if reports[0].Summary != "late result" {
		t.Fatalf("backfill must refresh the queued summary, got %q", reports[0].Summary)
	}
	if reports[0].Status != StatusCompleted {
		t.Fatalf("first terminal transition wins, want completed, got %s", reports[0].Status)
	}
	if again := m.DrainIdleReports(); len(again) != 0 {
		t.Fatalf("consume-once broken: second drain returned %d", len(again))
	}
	select {
	case <-m.IdleWakeCh():
		t.Fatal("wake edge must be consumed by the drain")
	default:
	}
}

func TestIssue5700CancelEnqueuesReport(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	id := m.Spawn("worker", "task", "task", nil, context.Background())

	if !m.Cancel(id) {
		t.Fatal("Cancel must succeed on a pending agent")
	}
	reports := m.DrainIdleReports()
	if len(reports) != 1 || reports[0].Status != StatusCancelled {
		t.Fatalf("cancel transition must enqueue one cancelled report, got %+v", reports)
	}
}

// #5700 cancel-silence: explicit parent cancel drops pending reports and
// disarms the queue; the next user-initiated submission re-arms it.
func TestIssue5700CancelSilenceDropsPendingAndDisarms(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	id1 := m.Spawn("a", "task", "task", nil, context.Background())
	m.Complete(id1, "done", nil)

	m.SilenceIdleReports()

	if reports := m.DrainIdleReports(); len(reports) != 0 {
		t.Fatalf("silence must drop pending reports, got %d", len(reports))
	}
	select {
	case <-m.IdleWakeCh():
		t.Fatal("silence must clear the wake edge")
	default:
	}

	id2 := m.Spawn("b", "task", "task", nil, context.Background())
	m.Complete(id2, "done-b", nil)
	if pending, _ := m.IdleReportStats(); pending != 0 {
		t.Fatalf("silenced queue must not accept new reports, pending=%d", pending)
	}

	m.ArmIdleReports()
	id3 := m.Spawn("c", "task", "task", nil, context.Background())
	m.Complete(id3, "done-c", nil)
	if pending, _ := m.IdleReportStats(); pending != 1 {
		t.Fatalf("armed queue must accept new reports, pending=%d", pending)
	}
}

// #5700 multi-child backpressure: the queue is bounded; overflow drops the
// newest report and counts it instead of blocking or growing unbounded.
func TestIssue5700QueueBoundDropsNewestWithCounter(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()

	total := maxIdleReports + 2
	for i := 0; i < total; i++ {
		id := m.Spawn("w", "task", "task", nil, context.Background())
		m.Complete(id, "r", nil)
	}
	pending, dropped := m.IdleReportStats()
	if pending != maxIdleReports {
		t.Fatalf("queue must hold at most %d reports, got %d", maxIdleReports, pending)
	}
	if dropped != total-maxIdleReports {
		t.Fatalf("overflow must count drops, want %d, got %d", total-maxIdleReports, dropped)
	}
	if reports := m.DrainIdleReports(); len(reports) != maxIdleReports {
		t.Fatalf("drain must return the bounded batch, got %d", len(reports))
	}
}

// #5700 finished-parent silence: only reports arriving after the cutoff
// wake the idle parent; older reports stay queued for the next run's
// in-run delivery (mid-turn enqueue not lost, not self-waking).
func TestIssue5700DrainSinceSplitsFreshAndStale(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()

	id1 := m.Spawn("stale", "task", "task", nil, context.Background())
	m.Complete(id1, "old", nil)
	cutoff := time.Now()
	id2 := m.Spawn("fresh", "task", "task", nil, context.Background())
	m.Complete(id2, "new", nil)

	fresh := m.DrainIdleReportsSince(cutoff)
	if len(fresh) != 1 || fresh[0].ID != id2 {
		t.Fatalf("drain-since must consume only fresh reports, got %+v", fresh)
	}
	pending, _ := m.IdleReportStats()
	if pending != 1 {
		t.Fatalf("stale report must stay queued for in-run delivery, pending=%d", pending)
	}
	kept := m.DrainIdleReports()
	if len(kept) != 1 || kept[0].ID != id1 {
		t.Fatalf("stale report must survive the fresh split, got %+v", kept)
	}
}

func TestIssue5700ConsumeIdleWakeKeepsQueue(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	id := m.Spawn("w", "task", "task", nil, context.Background())
	m.Complete(id, "done", nil)

	m.ConsumeIdleWake()

	select {
	case <-m.IdleWakeCh():
		t.Fatal("wake edge must be consumed")
	default:
	}
	if reports := m.DrainIdleReports(); len(reports) != 1 {
		t.Fatalf("consuming the wake edge must not drop queued reports, got %d", len(reports))
	}
}

// #5700: spawn rejections are terminal transitions too - the parent must
// learn its spawn bounced without polling.
func TestIssue5700SpawnRejectionEnqueuesReport(t *testing.T) {
	m := NewManager(config.SubAgentConfig{MaxConcurrent: 1})
	defer m.Shutdown()
	m.Spawn("first", "task", "task", nil, context.Background())
	rejected := m.Spawn("second", "task", "task", nil, context.Background())

	if !IsSyntheticID(rejected) {
		t.Fatalf("second spawn over the concurrent limit must be rejected, got id %q", rejected)
	}
	reports := m.DrainIdleReports()
	if len(reports) != 1 || reports[0].ID != rejected || reports[0].Status != StatusFailed {
		t.Fatalf("spawn rejection must enqueue one failed report, got %+v", reports)
	}
	if !strings.Contains(reports[0].Summary, "concurrent sub-agent limit") {
		t.Fatalf("rejection report must carry the reason, got %q", reports[0].Summary)
	}
}

// #5700 summary bound: queued reports carry a bounded summary, not the
// full (possibly 100KB) result.
func TestIssue5700SummaryBounded(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	id := m.Spawn("w", "task", "task", nil, context.Background())

	big := strings.Repeat("x", idleReportSummaryRunes*4)
	m.Complete(id, big, nil)

	reports := m.DrainIdleReports()
	if len(reports) != 1 {
		t.Fatalf("want 1 report, got %d", len(reports))
	}
	if got := len([]rune(reports[0].Summary)); got > idleReportSummaryRunes+20 {
		t.Fatalf("summary must be bounded to ~%d runes, got %d", idleReportSummaryRunes, got)
	}
}
