package swarm

import (
	"strconv"
	"testing"

	"github.com/topcheer/ggcode/internal/task"
)

// Restart-storm loop fix: ctxCancelRollbackTask must park the task at the
// same retry ceiling as the failure and panic paths. Before the fix it
// rolled back to pending unconditionally, so every restart re-claimed and
// re-ran the same task forever (attempts climbed past the cap unseen).

func TestCtxCancelRollbackParksAtRetryCap(t *testing.T) {
	tmMgr := task.NewManager()
	tk := tmMgr.Create("subject", "desc", "", nil)
	inProgress := task.StatusInProgress
	if _, err := tmMgr.Update(tk.ID, task.UpdateOptions{Status: &inProgress, Owner: ptr("tm-9")}); err != nil {
		t.Fatalf("seed in_progress: %v", err)
	}
	tm := &Teammate{ID: "tm-9"}
	claimed, _ := tmMgr.Get(tk.ID)

	// Simulate the storm: cancel-rollback at attempts = max (park expected).
	claimed.Metadata = map[string]string{"retry_attempts": strconv.Itoa(maxTransientTaskRetries)}
	ctxCancelRollbackTask(tmMgr, claimed, tm)

	after, ok := tmMgr.Get(tk.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if after.Status != task.StatusCompleted {
		t.Fatalf("expected completed(max_retries) park, got %s", after.Status)
	}
	if after.Metadata["permanent_error"] != "max_retries_exceeded" {
		t.Fatalf("expected permanent_error marker, got %v", after.Metadata)
	}
}

func TestCtxCancelRollbackUnderCapStaysPending(t *testing.T) {
	tmMgr := task.NewManager()
	tk := tmMgr.Create("subject", "desc", "", nil)
	inProgress := task.StatusInProgress
	if _, err := tmMgr.Update(tk.ID, task.UpdateOptions{Status: &inProgress, Owner: ptr("tm-9")}); err != nil {
		t.Fatalf("seed in_progress: %v", err)
	}
	tm := &Teammate{ID: "tm-9"}
	claimed, _ := tmMgr.Get(tk.ID)

	claimed.Metadata = map[string]string{"retry_attempts": "1"}
	ctxCancelRollbackTask(tmMgr, claimed, tm)

	after, _ := tmMgr.Get(tk.ID)
	if after.Status != task.StatusPending {
		t.Fatalf("expected pending (resumable) below the cap, got %s", after.Status)
	}
	if after.Metadata["retry_attempts"] != "2" {
		t.Fatalf("expected attempts incremented to 2, got %v", after.Metadata["retry_attempts"])
	}
}

func ptr(s string) *string { return &s }
