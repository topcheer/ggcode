package tool

import (
	"context"
	"testing"
	"time"
)

func TestCommandJobManagerLifecycle(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "printf 'one\\ntwo\\n'", 5*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}
	if started == nil || started.ID == "" {
		t.Fatal("expected command job id")
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait command job: %v", err)
	}
	if snap.Status != CommandJobCompleted {
		t.Fatalf("expected completed status, got %s", snap.Status)
	}
	if snap.TotalLines != 2 {
		t.Fatalf("expected 2 lines, got %d", snap.TotalLines)
	}
	if len(snap.Lines) != 2 || snap.Lines[0] != "one" || snap.Lines[1] != "two" {
		t.Fatalf("unexpected output lines: %#v", snap.Lines)
	}
}

func TestCommandJobManagerStop(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "sleep 5", 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job: %v", err)
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait stopped command job: %v", err)
	}
	if snap.Status != CommandJobCancelled {
		t.Fatalf("expected cancelled status, got %s", snap.Status)
	}
}

func TestCommandJobManagerOwnerContextCancellationDoesNotStopJob(t *testing.T) {
	// After the fix, start_command uses context.Background() internally so
	// that cancelling the caller's context does NOT kill the background
	// process. The process must be stopped explicitly via Stop().
	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(ctx, "sleep 5", 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	cancel()

	// Give a brief moment for any potential (now absent) cancellation to propagate.
	time.Sleep(100 * time.Millisecond)

	// The process should still be running.
	snap, err := mgr.Read(started.ID, 10, 0)
	if err != nil {
		t.Fatalf("read command job: %v", err)
	}
	if snap.Status != CommandJobRunning {
		t.Fatalf("expected running status after owner context cancel, got %s", snap.Status)
	}

	// Clean up: stop the job explicitly.
	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job: %v", err)
	}
}

func TestCommandJobManagerWaitIgnoresCancelledContext(t *testing.T) {
	// After the fix, wait_command does not propagate request context
	// cancellation. It waits for the specified duration or job completion.
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "sleep 5", 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	// Wait with an already-cancelled context should still succeed.
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()

	snap, err := mgr.Wait(waitCtx, started.ID, 1*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait should not error on cancelled context: %v", err)
	}
	if snap.Status != CommandJobRunning {
		t.Fatalf("expected running, got %s", snap.Status)
	}

	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job: %v", err)
	}
}

func TestCommandJobManagerWriteInput(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "read line; printf 'echo:%s\\n' \"$line\"", 5*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	if _, err := mgr.Write(started.ID, "hello async", true); err != nil {
		t.Fatalf("write command input: %v", err)
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait command job: %v", err)
	}
	if snap.Status != CommandJobCompleted {
		t.Fatalf("expected completed status, got %s", snap.Status)
	}
	if len(snap.Lines) != 1 || snap.Lines[0] != "echo:hello async" {
		t.Fatalf("unexpected output lines: %#v", snap.Lines)
	}
}

func TestCommandJobManagerWriteInputFailsForStoppedJob(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "printf 'done\\n'", 5*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	if _, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0); err != nil {
		t.Fatalf("wait command job: %v", err)
	}
	if _, err := mgr.Write(started.ID, "late input", true); err == nil {
		t.Fatal("expected write to completed command job to fail")
	}
}
