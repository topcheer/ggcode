package tool

// zz_issue4431_test.go - companion tests for #4431:
// 1) Write blocks forever when the child never reads stdin (full pipe) and
//    ctx cancellation cannot interrupt a raw Write; the fix makes Write
//    ctx-aware and closes the pipe to unblock+reap the writer goroutine.
// 2) stop_command on an already completed job silently succeeded, violating
//    its own documented contract ("returns an error"); the fix honors it.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestIssue4431WriteCancelledCtxUnblocks(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "sleep 30", false, 60*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}
	defer mgr.Stop(started.ID)

	// 128KB into a child that never reads stdin: the pipe buffer (64KB)
	// fills and the write blocks. Pre-cancelled ctx must return promptly
	// instead of hanging the tool call forever.
	wctx, cancel := context.WithCancel(context.Background())
	cancel()
	big := strings.Repeat("x", 128*1024)

	type res struct {
		err error
	}
	done := make(chan res, 1)
	go func() {
		_, werr := mgr.Write(wctx, started.ID, big, false)
		done <- res{werr}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("expected context error from cancelled write, got nil")
		}
		if !strings.Contains(r.err.Error(), "context canceled") {
			t.Fatalf("expected context canceled error, got: %v", r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("write on cancelled ctx blocked forever (issue #4431 bug 1)")
	}
}

func TestIssue4431StopCompletedJobReturnsError(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "echo hi", false, 10*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 5*time.Second, 10, 0)
	if err != nil {
		t.Fatalf("wait for completion: %v", err)
	}
	if snap.Status == CommandJobRunning {
		t.Fatalf("job should have completed, status: %s", snap.Status)
	}

	_, err = mgr.Stop(started.ID)
	if err == nil {
		t.Fatalf("stop on completed job must return an error per its documented contract (issue #4431 bug 2)")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Fatalf("unexpected error: %v", err)
	}
}
