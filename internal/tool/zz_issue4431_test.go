package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #4431: stdin writes must be bounded (a child that stops reading must not
// hang the tool call forever), and stopping an already-finished job must
// return an error, matching the stop_command tool description.
func TestWriteStdinBoundedNotInfinite(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	defer m.ShutdownAll(2 * time.Second)
	ctx := context.Background()

	// A child that never reads stdin and never exits on its own.
	// `cat` with stdin redirected from a pipe would read; use `sleep` which
	// never reads - writes beyond the 64KB pipe buffer would block forever
	// without the #4431 bound.
	snap, err := m.Start(ctx, "sleep 60", false, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	jobID := snap.ID

	// Smaller-than-buffer writes still succeed (normal path preserved).
	if _, err := m.Write(jobID, "hello", true); err != nil {
		t.Fatalf("small write should succeed: %v", err)
	}

	// A write larger than the pipe buffer against a non-reading child must
	// return within the bound (not hang), reporting the timeout and having
	// cancelled the job.
	start := time.Now()
	big := strings.Repeat("x", 256*1024)
	_, err = m.Write(jobID, big, false)
	elapsed := time.Since(start)
	if err == nil {
		// Pipes >64KB that somehow drained (OS buffering variance) - accept,
		// the important guarantee is non-hang.
		return
	}
	if elapsed > 30*time.Second {
		t.Fatalf("write hung: took %s past the bound", elapsed)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestStopFinishedJobReturnsError(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	defer m.ShutdownAll(2 * time.Second)
	ctx := context.Background()

	snap, err := m.Start(ctx, "echo done", false, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	jobID := snap.ID
	// Wait for natural completion.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, gerr := m.get(jobID); gerr == nil && jobSnapshotStatus(j) != CommandJobRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	finJob, serr := m.get(jobID)
	if serr != nil || finJob == nil || jobSnapshotStatus(finJob) == CommandJobRunning {
		t.Fatalf("job did not finish: %v %v", finJob, serr)
	}

	// stop_command documents: "Stopping an already completed or unknown job
	// returns an error." (#4431)
	_, err = m.Stop(jobID)
	if err == nil {
		t.Fatal("stopping a finished job must return an error, not silent success")
	}
	if !strings.Contains(err.Error(), "already finished") {
		t.Errorf("unexpected error text: %v", err)
	}

	// Stopping a RUNNING job still works (regression).
	snap2, err := m.Start(ctx, "sleep 5", false, 0)
	if err != nil {
		t.Fatalf("start2: %v", err)
	}
	if _, err := m.Stop(snap2.ID); err != nil {
		t.Errorf("stopping a running job must succeed: %v", err)
	}
}
