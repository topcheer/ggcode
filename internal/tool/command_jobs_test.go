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

func TestCommandJobManagerOwnerContextCancellationStopsJobImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(ctx, "sleep 5", 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	cancel()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	snap, err := mgr.Wait(waitCtx, started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait cancelled command job: %v", err)
	}
	if snap.Status != CommandJobCancelled {
		t.Fatalf("expected cancelled status after owner context cancel, got %s", snap.Status)
	}
}

func TestCommandJobManagerWaitRespectsCancellation(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "sleep 5", 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := mgr.Wait(waitCtx, started.ID, 30*time.Second, 20, 0); err == nil {
		t.Fatal("expected wait to stop on cancelled context")
	}

	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job after cancelled wait: %v", err)
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
