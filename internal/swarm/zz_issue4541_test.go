package swarm

import (
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

func TestIssue4541_CtxCancelSparesTerminalTask(t *testing.T) {
	team := &Team{
		ID:        "team-4541",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{},
		Tasks:     task.NewManager(),
	}
	tk := team.Tasks.Create("approved-task", "externally approved mid-run", "", nil)

	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusInProgress)}); err != nil {
		t.Fatal(err)
	}
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}

	claimed, _ := team.Tasks.Get(tk.ID)
	tm := &Teammate{ID: "tm-4541", Name: "worker"}

	ctxCancelRollbackTask(team.Tasks, claimed, tm)

	got, ok := team.Tasks.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("#4541: externally-completed task flipped to %v by unguarded ctx-cancel rollback (revive-and-rerun bug)", got.Status)
	}
	if _, injected := got.Metadata["retry_attempts"]; injected {
		t.Fatalf("#4541: retry_attempts injected into completed task metadata: %v", got.Metadata)
	}
}

func TestIssue4541_CtxCancelRollsBackRunningTask(t *testing.T) {
	team := &Team{
		ID:        "team-4541b",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{},
		Tasks:     task.NewManager(),
	}
	tk := team.Tasks.Create("running-task", "interrupted by shutdown", "", nil)
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusInProgress), Metadata: map[string]string{"retry_attempts": "1"}}); err != nil {
		t.Fatal(err)
	}
	claimed, _ := team.Tasks.Get(tk.ID)
	tm := &Teammate{ID: "tm-4541b", Name: "worker"}

	ctxCancelRollbackTask(team.Tasks, claimed, tm)

	got, ok := team.Tasks.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if got.Status != task.StatusPending {
		t.Fatalf("#4541: in_progress task must roll back to pending on ctx cancel, got %v", got.Status)
	}
	if got.Owner != "" {
		t.Fatalf("#4541: rollback must clear owner, got %q", got.Owner)
	}
	if got.Metadata["retry_attempts"] != "2" {
		t.Fatalf("#4541: retry_attempts must increment (1->2), got %q", got.Metadata["retry_attempts"])
	}
}
