package a2a

import (
	"context"
	"testing"
	"time"
)

// TestHandleCancelEntryInstalledBeforePublish verifies the publish/cancel-entry
// ordering invariant in Handle: whenever a non-terminal task is visible in
// h.tasks, its cancel entry must already exist in h.cancels.
//
// Regression guard for the window where Handle published the task and released
// the lock before installing the cancel entry: CancelTask in that window marked
// the task Canceled but found no entry to call, so taskCtx was never cancelled
// and the execute goroutine ran the full skill (with side effects) on a task
// the client had already seen as canceled.
//
// The window is only microseconds wide, so the poll-based check below is a
// canary rather than a deterministic repro; the structural fix is that
// installCancelLocked now runs inside the same critical section as the map
// insert, making violation of the invariant impossible.
func TestHandleCancelEntryInstalledBeforePublish(t *testing.T) {
	const iterations = 50
	for i := 0; i < iterations; i++ {
		h := NewTaskHandler(t.TempDir(), nil, nil,
			WithTimeout(10*time.Second), WithMaxTasks(8))

		go func() {
			_, _ = h.Handle(context.Background(), SkillFileSearch,
				Message{Parts: []Part{{Kind: "text", Text: "x"}}}, "")
		}()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			var (
				id      string
				hasEntr bool
			)
			for tid, tsk := range h.tasks {
				if tsk.Status.IsTerminal() {
					continue
				}
				id = tid
				_, hasEntr = h.cancels[tid]
			}
			h.mu.Unlock()

			if id == "" {
				time.Sleep(200 * time.Microsecond)
				continue
			}
			if !hasEntr {
				t.Fatalf("iteration %d: non-terminal task %s visible without cancel entry (publish-before-install race regressed)", i, id)
			}
			break
		}
	}
}
