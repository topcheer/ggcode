package tool

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// TestAutoBackgroundFinishCancelsCtx covers #659: auto-background jobs whose
// command reaches a terminal state quickly must have their Background-derived
// WithTimeout ctx cancelled by finish(). Before the fix, finish() nilled
// j.cancel without calling it, leaking the timer/ctx tree until timeout.
func TestAutoBackgroundFinishCancelsCtx(t *testing.T) {
	m := NewCommandJobManager("")
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	cmd, _, err := util.NewShellCommandContext(ctx, "echo hi")
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	job, snapshot, err := m.StartExisting(ctx, cmd, "echo hi", time.Hour, cancel)
	if err != nil {
		t.Fatalf("StartExisting: %v", err)
	}
	if snapshot != nil && snapshot.Status == CommandJobFailed {
		t.Fatalf("job failed to start: %s", snapshot.ErrText)
	}
	if job == nil {
		t.Fatal("nil job")
	}

	// Wait for the quick command to reach a terminal state (watchdog avoided).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := m.snapshot(job)
		if snap.Status != CommandJobRunning {
			// The shared ctx (parent of the job's process ctx here is the same
			// ctx we passed) must now be cancelled by finish().
			select {
			case <-ctx.Done():
				return // pass: cancel was invoked
			case <-time.After(2 * time.Second):
				t.Fatalf("job finished with status %s but ctx was not cancelled — timer leak (#659)", snap.Status)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("command did not reach terminal state within 5s")
}
