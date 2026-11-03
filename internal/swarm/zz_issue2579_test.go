package swarm

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

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
