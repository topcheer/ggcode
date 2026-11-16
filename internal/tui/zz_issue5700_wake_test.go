package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func new5700ModelWithManager(t *testing.T) (Model, *subagent.Manager) {
	t.Helper()
	m := newTestModel()
	mgr := subagent.NewManager(config.SubAgentConfig{})
	t.Cleanup(mgr.Shutdown)
	m.subAgentMgr = mgr
	return m, mgr
}

// #5700 idle wake: N children finishing while the parent is idle produce
// exactly ONE digest turn, not one turn per report.
func TestIssue5700IdleWakeCoalescesConcurrentReports(t *testing.T) {
	m, mgr := new5700ModelWithManager(t)
	id1 := mgr.Spawn("alpha", "task", "task", nil, context.Background())
	id2 := mgr.Spawn("beta", "task", "task", nil, context.Background())
	mgr.Complete(id1, "result-alpha", nil)
	mgr.Complete(id2, "result-beta", nil)

	m2, _ := m.handleSubAgentDoneMsg(subAgentDoneMsg{AgentID: id1, AgentName: "alpha", Kind: "subagent"})

	if !strings.Contains(m2.lastUserSubmission, "idle") {
		t.Fatalf("first done msg while idle must submit a wake turn, got %q", m2.lastUserSubmission)
	}
	if !strings.Contains(m2.lastUserSubmission, id1) || !strings.Contains(m2.lastUserSubmission, id2) {
		t.Fatalf("wake digest must batch both concurrent reports, got %q", m2.lastUserSubmission)
	}

	m3, _ := m2.handleSubAgentDoneMsg(subAgentDoneMsg{AgentID: id2, AgentName: "beta", Kind: "subagent"})
	if m3.lastUserSubmission != m2.lastUserSubmission {
		t.Fatalf("second done msg must not submit a second wake turn: %q != %q", m3.lastUserSubmission, m2.lastUserSubmission)
	}
	if pending, _ := mgr.IdleReportStats(); pending != 0 {
		t.Fatalf("wake drain must empty the queue, pending=%d", pending)
	}
}

// #5700 mid-turn delivery: while the parent is mid-run the done msg must
// not submit anything; the report stays queued for the in-run completion
// feed (consume-once drain).
func TestIssue5700BusyParentDefersToInRunDelivery(t *testing.T) {
	m, mgr := new5700ModelWithManager(t)
	m.setLoading(true)
	id := mgr.Spawn("alpha", "task", "task", nil, context.Background())
	mgr.Complete(id, "result", nil)

	m2, _ := m.handleSubAgentDoneMsg(subAgentDoneMsg{AgentID: id, AgentName: "alpha", Kind: "subagent"})

	if m2.lastUserSubmission != "" {
		t.Fatalf("busy parent must not be woken, submitted %q", m2.lastUserSubmission)
	}
	pending, _ := mgr.IdleReportStats()
	if pending != 1 {
		t.Fatalf("report must stay queued for in-run delivery, pending=%d", pending)
	}

	feed := subagentCompletionFeed(mgr)
	completions := feed()
	if len(completions) != 1 || completions[0].ID != id || completions[0].Summary != "result" {
		t.Fatalf("in-run feed must drain the queued report once, got %+v", completions)
	}
	if again := feed(); len(again) != 0 {
		t.Fatalf("feed drain must be consume-once, got %d", len(again))
	}
}

// #5700 finished-parent silence: reports already pending when the previous
// run ended must not self-wake the idle parent; they stay queued for the
// next run's in-run delivery.
func TestIssue5700StaleReportDoesNotWakeFinishedParent(t *testing.T) {
	m, mgr := new5700ModelWithManager(t)
	id := mgr.Spawn("alpha", "task", "task", nil, context.Background())
	mgr.Complete(id, "result", nil)
	m.runEndedAt = time.Now()

	m2, _ := m.handleSubAgentDoneMsg(subAgentDoneMsg{AgentID: id, AgentName: "alpha", Kind: "subagent"})

	if m2.lastUserSubmission != "" {
		t.Fatalf("report pending at run end must not wake the finished parent, submitted %q", m2.lastUserSubmission)
	}
	if pending, _ := mgr.IdleReportStats(); pending != 1 {
		t.Fatalf("stale report must stay queued (not lost), pending=%d", pending)
	}
}

// #5700 cancel-silence at the surface: while CancelAll is in flight the
// done msg must never start a new run.
func TestIssue5700CancelingSuppressesWake(t *testing.T) {
	m, mgr := new5700ModelWithManager(t)
	m.subAgentsCanceling = true
	id := mgr.Spawn("alpha", "task", "task", nil, context.Background())
	mgr.Complete(id, "result", nil)

	m2, _ := m.handleSubAgentDoneMsg(subAgentDoneMsg{AgentID: id, AgentName: "alpha", Kind: "subagent"})

	if m2.lastUserSubmission != "" {
		t.Fatalf("canceling window must suppress the wake, submitted %q", m2.lastUserSubmission)
	}
}

// #5700 teammate wake keeps its legacy direct-hint shape (swarm has its
// own event bus, outside the subagent idle-report queue).
func TestIssue5700TeammateWakeKeepsDirectHint(t *testing.T) {
	m, _ := new5700ModelWithManager(t)
	m2, _ := m.handleSubAgentDoneMsg(subAgentDoneMsg{AgentID: "tm1", AgentName: "worker", Kind: "teammate"})

	if !strings.Contains(m2.lastUserSubmission, "worker") {
		t.Fatalf("teammate done must keep the direct agentHint wake, got %q", m2.lastUserSubmission)
	}
}
