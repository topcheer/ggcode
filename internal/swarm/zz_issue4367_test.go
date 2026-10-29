package swarm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

// #4367 regression pin: the CurrentTaskID write must precede the first
// panicable call (teammateConsumeProbe / onEvent / prompt building) in the
// claim sequence. The panic-recovery rollback (rollbackClaimedTask, pinned
// by TestRollbackClaimedTask) keys off tm.CurrentTaskID; when the write sat
// AFTER those calls, a panic in the window rolled back a no-op and the task
// leaked in_progress forever (#1688 regression). An e2e panic harness is not
// feasible (see the KNOWN LIMIT note in idle_runner_test.go), so this pins
// the source order of the two statements instead - if someone moves the
// write back after the probe, this fails.
func TestIssue4367CurrentTaskIDWrittenBeforePanicableCalls(t *testing.T) {
	src, err := os.ReadFile("idle_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	writeIdx := strings.Index(s, "tm.CurrentTaskID = claimed.ID")
	probeIdx := strings.Index(s, "teammateConsumeProbe(tm.ID)")
	if writeIdx < 0 {
		t.Fatal("CurrentTaskID write to claimed.ID not found in idle_runner.go")
	}
	if probeIdx < 0 {
		t.Fatal("teammateConsumeProbe call not found")
	}
	if writeIdx > probeIdx {
		t.Fatal("#4367 regression: CurrentTaskID write comes AFTER teammateConsumeProbe - the panic-window rollback is dead again")
	}
}

// TestIssue4367RollbackPathRevertsClaimedTask double-pins the rollback
// semantics the early write depends on: with CurrentTaskID set, the task
// returns to pending and is claimable again.
func TestIssue4367RollbackPathRevertsClaimedTask(t *testing.T) {
	mgr := newTestManager()
	snap := mgr.CreateTeam("t4367", "leader")
	defer func() { _ = mgr.DeleteTeam(snap.ID) }()
	mgr.mu.Lock()
	team := mgr.teams[snap.ID]
	mgr.mu.Unlock()

	board, err := mgr.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := board.Create("t4367", "d", "", nil)
	inProgress := task.StatusInProgress
	pending := task.StatusPending
	owner := "tm-4367"
	if _, err := board.Update(created.ID, task.UpdateOptions{Status: &inProgress, Owner: &owner}); err != nil {
		t.Fatal(err)
	}
	tm := &Teammate{ID: owner}
	tm.mu.Lock()
	tm.CurrentTaskID = created.ID
	tm.mu.Unlock()
	rollbackClaimedTask(mgr, team, tm)
	got, ok := board.Get(created.ID)
	if !ok || got.Status != pending {
		t.Fatalf("after rollback task must be pending, got %+v ok=%v", got, ok)
	}
	// And it is claimable again by another owner.
	owner2 := "tm2"
	if _, err := board.Update(created.ID, task.UpdateOptions{ExpectedStatus: &pending, Status: &inProgress, Owner: &owner2}); err != nil {
		t.Fatalf("re-claim after rollback failed: %v", err)
	}
}
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

	// The loop's #1688 recover handler rolls the task back and does NOT
	// re-panic; the observable escape here is this onEvent panicking again
	// when the handler emits teammate_shutdown - the panic escapes the
	// deferred recover, letting the outer wrapper observe the post-rollback
	// state.
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
