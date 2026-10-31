package swarm

// #4541 regression: the ctx-cancel rollback (idle loop's counterpart of
// rollbackClaimedTask) must be guarded with ExpectedStatus=in_progress, as
// #2579 established for the panic path. Before the fix the unguarded write
// flipped a COMPLETED task (set terminal externally during executeTask by a
// review approve, a manual /task command, or a durable-board snapshot
// restore) back to pending - completed tasks were re-claimed and re-executed,
// re-running LLM work and side effects over the persisted result. It must
// also carry retry_attempts forward (#2118) on the legitimate rollback.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// Guard probe: a task set terminal externally while executeTask ran must
// survive ctx-cancel rollback untouched.
func TestIssue4541_CtxCancelSparesTerminalTask(t *testing.T) {
	team := &Team{
		ID:        "team-4541",
		Name:      "test",
		LeaderID:  "leader",
		Teammates: map[string]*Teammate{},
		Tasks:     task.NewManager(),
	}
	tk := team.Tasks.Create("approved-task", "externally approved mid-run", "", nil)
	// Drive to in_progress (claimed), then an external writer completes it.
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusInProgress)}); err != nil {
		t.Fatal(err)
	}
	if _, err := team.Tasks.Update(tk.ID, task.UpdateOptions{Status: strPtr(task.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}
	// The teammate still holds the claim snapshot from claim time.
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

// Legitimate path: a task still in_progress when ctx cancels must roll back
// to pending, unassigned, with retry_attempts incremented.
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
