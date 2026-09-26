package swarm

// #2786 regression: the two parking paths (quota/auth permanent failure,
// and the #1295 max-retries cap) mark a task StatusCompleted with a
// permanent_error metadata key so it is never re-claimed. But
// allBlockersComplete only compared the literal status, so a parked
// blocker unlocked its BlockedBy dependents to execute on outputs that
// were never produced.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// Guard probe: a quota-parked blocker (completed + permanent_error) must
// keep its dependents blocked.
func TestIssue2786_ParkedBlockerKeepsDependentsBlocked(t *testing.T) {
	tmMgr := task.NewManager()

	blocker := tmMgr.Create("blocker", "quota dead", "", nil)
	if _, err := tmMgr.Update(blocker.ID, task.UpdateOptions{
		Status:   strPtr(task.StatusCompleted),
		Metadata: map[string]string{"permanent_error": "quota", "error": "429"},
	}); err != nil {
		t.Fatal(err)
	}

	dependent := tmMgr.Create("dependent", "needs blocker output", "", nil)
	dependent, err := tmMgr.Update(dependent.ID, task.UpdateOptions{
		AddBlockedBy: []string{blocker.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if allBlockersComplete(tmMgr, dependent) {
		t.Fatal("#2786: parked blocker (completed+permanent_error) must keep dependents blocked - they would execute on nonexistent output")
	}
}

// Guard probe: the max-retries parking path uses the same metadata key.
func TestIssue2786_MaxRetriesParkingKeepsDependentsBlocked(t *testing.T) {
	tmMgr := task.NewManager()

	blocker := tmMgr.Create("blocker", "poison task", "", nil)
	if _, err := tmMgr.Update(blocker.ID, task.UpdateOptions{
		Status: strPtr(task.StatusCompleted),
		Metadata: map[string]string{
			"permanent_error": "max_retries_exceeded",
			"retry_attempts":  "3",
		},
	}); err != nil {
		t.Fatal(err)
	}

	dependent := tmMgr.Create("dependent", "waits", "", nil)
	dependent, err := tmMgr.Update(dependent.ID, task.UpdateOptions{
		AddBlockedBy: []string{blocker.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if allBlockersComplete(tmMgr, dependent) {
		t.Fatal("#2786: max_retries-parked blocker must keep dependents blocked")
	}
}

// Genuine completion (no permanent_error) must still unblock dependents.
func TestIssue2786_GenuineCompletionStillUnblocks(t *testing.T) {
	tmMgr := task.NewManager()

	blocker := tmMgr.Create("blocker", "real work", "", nil)
	if _, err := tmMgr.Update(blocker.ID, task.UpdateOptions{Status: strPtr(task.StatusCompleted)}); err != nil {
		t.Fatal(err)
	}

	dependent := tmMgr.Create("dependent", "waits", "", nil)
	dependent, err := tmMgr.Update(dependent.ID, task.UpdateOptions{
		AddBlockedBy: []string{blocker.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !allBlockersComplete(tmMgr, dependent) {
		t.Fatal("genuine completion must unblock dependents (regression guard)")
	}
}
