package agentruntime

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
)

// #3288 regression. Staggered completion is the norm in real parallel runs:
// the pre-fix poll loop re-made snapsFinal every iteration and skipped
// terminal candidates, so earlier-terminal candidates' final snapshots were
// zero values at the break - ranking degraded to empty trajectories, winner
// result text vanished, and the auction-close leader baseline never matched.

// staggerSnaps: cand-1 is terminal (completed, real result) from poll 1;
// cand-2 stays Running for 2 polls then fails for real.
type staggerSnaps struct {
	cand2Polls atomic.Int32
}

func (f *staggerSnaps) Snapshot(id string) (subagent.Snapshot, bool) {
	switch id {
	case "cand-1":
		return doneSnap("cand-1", "ok: edit applied, tests pass", false), true
	case "cand-2":
		if f.cand2Polls.Add(1) < 3 {
			return subagent.Snapshot{ID: "cand-2", Status: subagent.StatusRunning}, true
		}
		return doneSnap("cand-2", "", true), true
	}
	return subagent.Snapshot{}, false
}
func (f *staggerSnaps) RunningCount() int { return 0 }

// Probe 1: the early-terminal candidate must keep its final snapshot into
// the ranking/report phase - no zero-value ID/Status, real result text, and
// a real (non-placeholder) trajectory.
func TestBONStaggeredEarlyFinalSnapshotSurvives(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &staggerSnaps{}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{
		Task: "fix bug X", N: 2, Name: "stagger", Poll: time.Millisecond,
	})
	if len(rep.Candidates) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(rep.Candidates))
	}
	var early *CandidateOutcome
	for i := range rep.Candidates {
		if rep.Candidates[i].ID == "cand-1" {
			early = &rep.Candidates[i]
		}
	}
	if early == nil {
		t.Fatalf("early candidate lost from report: %+v", rep.Candidates)
	}
	if early.Status != string(subagent.StatusCompleted) {
		t.Fatalf("early candidate status = %q, want completed", early.Status)
	}
	if early.Result != "ok: edit applied, tests pass" {
		t.Fatalf("early candidate result = %q, want real result text", early.Result)
	}
	if strings.Contains(rep.Report, "no trajectory events recorded") {
		t.Fatalf("ranking degraded to placeholder trajectory:\n%s", rep.Report)
	}
}

// Probe 2: same-round terminal snapshots (the shape existing tests already
// cover) must not regress.
func TestBONSameRoundTerminalsIntact(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": doneSnap("cand-1", "ok A", false),
		"cand-2": doneSnap("cand-2", "ok B", false),
	}}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{
		Task: "fix bug Y", N: 2, Name: "same", Poll: time.Millisecond,
	})
	for i, c := range rep.Candidates {
		if c.ID == "" || c.Status == "" || c.Result == "" {
			t.Fatalf("candidate %d degraded: %+v", i, c)
		}
	}
}

// staggerYieldSnaps: leader completes on poll 1 with ToolCallCount=2; a
// laggard keeps Running with ToolCallCount above the yield factor, then is
// cancelled. The auction-close scan must see the leader's REAL spend from
// the preserved terminal snapshot.
type staggerYieldSnaps struct {
	polls     atomic.Int32
	cancelled atomic.Bool
}

func (f *staggerYieldSnaps) Snapshot(id string) (subagent.Snapshot, bool) {
	switch id {
	case "cand-1":
		s := doneSnap("cand-1", "ok cheap win", false)
		s.ToolCallCount = 2
		return s, true
	case "cand-2":
		if f.cancelled.Load() {
			return subagent.Snapshot{ID: "cand-2", Status: subagent.StatusCancelled}, true
		}
		return subagent.Snapshot{ID: "cand-2", Status: subagent.StatusRunning, ToolCallCount: 10}, true
	}
	return subagent.Snapshot{}, false
}
func (f *staggerYieldSnaps) RunningCount() int { return 1 }
func (f *staggerYieldSnaps) Cancel(id string) bool {
	if id == "cand-2" {
		f.cancelled.Store(true)
		return true
	}
	return false
}

// Probe 3: auction-close leader baseline (#3070). The leader finished
// early; the preserved snapshot must let the scan see StatusCompleted +
// ToolCallCount and yield the over-spending laggard. Pre-fix, the re-made
// snapsFinal zeroed the leader, leaderSpend never went >= 0, and the close
// was a silent no-op.
func TestBONStaggeredAuctionCloseLeaderBaseline(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &staggerYieldSnaps{}
	rep := RunBestOfN(context.Background(), sp, sn, BestOfNOptions{
		Task: "fix bug Z", N: 2, Name: "yield", Poll: time.Millisecond,
		YieldOnFirstSuccess: true,
	})
	if !strings.Contains(rep.Evidence, "[auction-close]") {
		t.Fatalf("auction close never fired (leader baseline lost): evidence=%q", rep.Evidence)
	}
}
