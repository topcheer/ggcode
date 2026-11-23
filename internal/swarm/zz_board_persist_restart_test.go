package swarm

import (
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// Regression (field report 2026-11-23: "teammate re-picks finished tasks
// after restart"). The teammate idle loop mutates the board with plain
// tm.Update (claim -> in_progress, then completed) and never calls
// EmitBoardUpdated, which used to be the ONLY persistence trigger. So
// board.json on disk stayed at the creation snapshot (all pending) and the
// next RestoreTeam re-claimed and re-ran finished work.
//
// Reproduce at the board layer with the exact idle-loop call shape, then
// restore on a fresh Manager and assert the task is still completed.
func TestBoardPersist_TeammateCompletionSurvivesRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m1 := zz4781Manager(t)
	team := m1.CreateTeam("persist-team", "leader-1")
	tmA, err := m1.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := tmA.Create("finished work", "desc", "", nil)

	// Idle-loop claim + complete: plain Update, no EmitBoardUpdated, no
	// tool-layer involvement - exactly idle_runner.go:303 / :417.
	inProgress := task.StatusInProgress
	owner := "tm-1"
	if _, err := tmA.Update(created.ID, task.UpdateOptions{Status: &inProgress, Owner: &owner}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	completed := task.StatusCompleted
	if _, err := tmA.Update(created.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Restart: a fresh Manager restores the same (same-session) board.
	m2 := zz4781Manager(t)
	if _, err := m2.RestoreTeam(team.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	tmB, err := m2.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := tmB.Get(created.ID)
	if !ok {
		t.Fatal("task missing after restore")
	}
	if got.Status != task.StatusCompleted {
		t.Fatalf("finished task must stay completed across restart, got %q (it would be re-claimed and re-run)", got.Status)
	}
}

// The counter-case that must keep working: a task that was genuinely mid-flight
// when the process died is still rolled back to pending on restore so the work
// resumes instead of leaking in_progress forever.
func TestBoardPersist_InterruptedTaskStillRollsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m1 := zz4781Manager(t)
	team := m1.CreateTeam("rollback-team", "leader-1")
	tmA, err := m1.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	created := tmA.Create("interrupted work", "desc", "", nil)
	inProgress := task.StatusInProgress
	owner := "tm-1"
	if _, err := tmA.Update(created.ID, task.UpdateOptions{Status: &inProgress, Owner: &owner}); err != nil {
		t.Fatalf("claim: %v", err)
	}

	m2 := zz4781Manager(t)
	if _, err := m2.RestoreTeam(team.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	tmB, err := m2.EnsureTaskManager(team.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := tmB.Get(created.ID)
	if got.Status != task.StatusPending {
		t.Fatalf("interrupted task must roll back to pending on restore, got %q", got.Status)
	}
}
