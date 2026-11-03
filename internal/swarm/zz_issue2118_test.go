package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

func TestIssue2118_PanicRollbackCountsRetries(t *testing.T) {
	team := &Team{
		ID:        "team-2118",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{},
		Tasks:     task.NewManager(),
	}
	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams[team.ID] = team
	mgr.mu.Unlock()

	tk := team.Tasks.Create("poison-panic", "panics", "", nil)

	inProgress := task.StatusInProgress
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: &inProgress}); err != nil {
		t.Fatal(err)
	}
	tm := &Teammate{ID: "tm-1", Name: "worker", ctx: context.Background()}
	tm.mu.Lock()
	tm.CurrentTaskID = tk.ID
	tm.mu.Unlock()

	rollbackClaimedTask(mgr, team, tm)
	got, ok := team.Tasks.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if got.Status != task.StatusPending {
		t.Fatalf("after first rollback status = %v, want pending", got.Status)
	}
	if got.Metadata["retry_attempts"] != "1" {
		t.Fatalf("retry_attempts = %q, want 1 (panic path must count)", got.Metadata["retry_attempts"])
	}

	pending := task.StatusPending
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{
		ExpectedStatus: &pending,
		Status:         &inProgress,
		Metadata:       map[string]string{"retry_attempts": "2"},
	}); err != nil {
		t.Fatal(err)
	}
	rollbackClaimedTask(mgr, team, tm)
	got, ok = team.Tasks.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("cap-exceeded panic rollback must park the task, got status %v", got.Status)
	}
	if got.Metadata["permanent_error"] != "max_retries_exceeded" {
		t.Fatalf("parked task missing permanent_error marker: %v", got.Metadata)
	}
}

func TestIssue2118_RollbackVanishedTaskSafe(t *testing.T) {
	team := &Team{ID: "team-v", Tasks: task.NewManager()}
	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams[team.ID] = team
	mgr.mu.Unlock()
	tm := &Teammate{ID: "tm-2", ctx: context.Background()}
	tm.mu.Lock()
	tm.CurrentTaskID = "no-such-task"
	tm.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		rollbackClaimedTask(mgr, team, tm)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rollback on vanished task hung")
	}
}
