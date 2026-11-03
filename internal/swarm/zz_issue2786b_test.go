package swarm

import (
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

func TestIssue2786_MixedBlockersOneParkedStaysLocked(t *testing.T) {
	tmMgr := task.NewManager()

	good := tmMgr.Create("good", "real success", "", nil)
	completed := task.StatusCompleted
	if _, err := tmMgr.Update(good.ID, task.UpdateOptions{Status: &completed}); err != nil {
		t.Fatal(err)
	}
	bad := tmMgr.Create("bad", "quota dead", "", nil)
	if _, err := tmMgr.Update(bad.ID, task.UpdateOptions{
		Status:   &completed,
		Metadata: map[string]string{"permanent_error": "auth"},
	}); err != nil {
		t.Fatal(err)
	}

	dep := task.Task{ID: "dependent-mixed", Subject: "needs both", BlockedBy: []string{good.ID, bad.ID}}
	if allBlockersComplete(tmMgr, dep) {
		t.Fatal("#2786: dependent unlocked when one of several blockers is parked")
	}
}
