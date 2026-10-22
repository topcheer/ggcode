package swarm

// #4367 regression: the board claim was made atomic (pending->in_progress,
// owner set) but tm.CurrentTaskID was only written AFTER several calls that
// can panic (teammateConsumeProbe, the injected onEvent callback, prompt
// building). A panic inside that window hit the #1688 recovery defer, whose
// rollbackClaimedTask is keyed on tm.CurrentTaskID - still "" from the
// previous task's terminal cleanup - so the rollback was a no-op and the
// task stayed in_progress forever (claim only scans pending; BlockedBy
// dependents deadlock via allBlockersComplete). The fix writes
// CurrentTaskID immediately after a successful claim, BEFORE any
// panic-capable call.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

// TestIssue4367PanicInOnEventAfterClaimRollsBackToPending drives the real
// claim path with an onEvent callback that panics on the first event - the
// "team_board_updated" fired immediately after a successful claim, i.e. the
// exact #4367 window. The loop's recover must roll the claimed task back to
// pending; before the fix the rollback read an empty CurrentTaskID and the
// task leaked in_progress.
func TestIssue4367PanicInOnEventAfterClaimRollsBackToPending(t *testing.T) {
	agent := &countingAgent{}
	tm := &Teammate{
		ID:     "tm-1",
		Name:   "worker",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}

	taskMgr := task.NewManager()
	team := &Team{
		ID:        "team-1",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{"tm-1": tm},
		Tasks:     taskMgr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.ctx = ctx

	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams["team-1"] = team
	mgr.mu.Unlock()

	// Panics on the FIRST event: the post-claim "team_board_updated" - before
	// prompt building and before the (previously late) CurrentTaskID write.
	onEvent := func(ev Event) {
		panic("onEvent exploded in the #4367 window")
	}

	// The loop's #1688 recover handler rolls the task back and then
	// RE-PANICS (crash loudly after cleanup). Drive it through an outer
	// recovery wrapper so the test can observe the post-rollback state.
	repanicked := make(chan interface{}, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				repanicked <- r
			}
		}()
		runTeammateLoop(ctx, tm, team, agent, mgr, onEvent, 30*time.Minute)
	}()

	created := taskMgr.Create("probe", "probe description", "", nil)

	// Wait for the panic path to settle: recover rolls back, then repanics.
	select {
	case r := <-repanicked:
		if s, ok := r.(string); !ok || s != "onEvent exploded in the #4367 window" {
			t.Fatalf("unexpected repanic value: %v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not panic-recover within 5s")
	}

	// The claimed task must be back to pending (and unowned) - the rollback
	// was armed by the EARLY CurrentTaskID write.
	got, ok := taskMgr.Get(created.ID)
	if !ok {
		t.Fatal("task vanished from board")
	}
	if got.Status != task.StatusPending {
		t.Fatalf("task status = %s, want pending - panic-window rollback failed (#4367)", got.Status)
	}
	if got.Owner != "" {
		t.Fatalf("task owner = %q, want empty after rollback", got.Owner)
	}

	// The agent must never have been invoked: the panic precedes execution.
	if calls := agent.getCalls(); calls != 0 {
		t.Fatalf("agent was invoked %d times, want 0 (panic must precede execution)", calls)
	}
}
