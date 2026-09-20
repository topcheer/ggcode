package swarm

// #2579 regression: the panic rollback must never act on a task that is
// already terminal. Before the fix, (a) CurrentTaskID stayed set after task
// completion, and (b) rollbackClaimedTask updated without an
// ExpectedStatus guard — so a panic in the teammate's idle window flipped a
// COMPLETED task back to pending for re-execution, or overwrote a
// successful task's metadata with panic metadata at the retry cap.

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// Guard probe: a completed task with a stale CurrentTaskID must survive
// rollback untouched (status stays completed, no retry_attempts injected,
// metadata not overwritten — including the parking branch).
func TestIssue2579_RollbackSparesCompletedTask(t *testing.T) {
	team := &Team{
		ID:        "team-2579",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{},
		Tasks:     task.NewManager(),
	}
	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams[team.ID] = team
	mgr.mu.Unlock()

	tk := team.Tasks.Create("done-task", "already finished", "", nil)
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusInProgress)}); err != nil {
		t.Fatal(err)
	}
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}

	// Stale CurrentTaskID pointing at the completed task (pre-#2579 leak).
	tm := &Teammate{ID: "tm-1", Name: "worker", ctx: context.Background()}
	tm.mu.Lock()
	tm.CurrentTaskID = tk.ID
	tm.mu.Unlock()

	rollbackClaimedTask(mgr, team, tm)

	got, ok := team.Tasks.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("#2579: completed task flipped to %v by stale panic rollback (guard missing?)", got.Status)
	}
	if _, injected := got.Metadata["retry_attempts"]; injected {
		t.Fatalf("#2579: retry_attempts injected into completed task metadata: %v", got.Metadata)
	}
}

// Guard probe for the parking branch: a completed task seeded at the retry
// cap must NOT have its success metadata overwritten by panic metadata.
func TestIssue2579_ParkingBranchSparesCompletedTask(t *testing.T) {
	team := &Team{
		ID:        "team-2579b",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{},
		Tasks:     task.NewManager(),
	}
	mgr := newTestManager()
	mgr.mu.Lock()
	mgr.teams[team.ID] = team
	mgr.mu.Unlock()

	tk := team.Tasks.Create("capped-done", "finished with transient retries", "", nil)
	// Drive: pending -> in_progress -> completed, carrying retry_attempts=2
	// (a task that transiently failed twice then succeeded).
	inProgress := task.StatusInProgress
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{ExpectedStatus: strPtr(task.StatusPending), Status: &inProgress}); err != nil {
		t.Fatal(err)
	}
	completed := task.StatusCompleted
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Metadata: map[string]string{"retry_attempts": "2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}

	tm := &Teammate{ID: "tm-2", Name: "worker", ctx: context.Background()}
	tm.mu.Lock()
	tm.CurrentTaskID = tk.ID
	tm.mu.Unlock()

	rollbackClaimedTask(mgr, team, tm)

	got, ok := team.Tasks.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("#2579: completed task flipped to %v by parking branch", got.Status)
	}
	if got.Metadata["error"] == "teammate panicked repeatedly" {
		t.Fatalf("#2579: success task metadata overwritten by panic metadata: %v", got.Metadata)
	}
}

func strPtr(s task.TaskStatus) *task.TaskStatus { return &s }
