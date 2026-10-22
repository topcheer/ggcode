package stream

// #4352 regression: WriteFrame must not hold e.mu across the blocking
// stdin write. Before the fix, a frame larger than the pipe buffer (a
// 1080p RGBA frame is ~8.3MB vs a 64KB pipe) wedged every other lock
// user: Read snapshots, IsRunning, and the monitor's reap path, so
// Stop's graceful-flush window degraded to the full Kill timeout.
//
// Probes use a real (non-started) Encoder whose stdin is an os.Pipe
// pre-filled to capacity, making WriteFrame block exactly like a real
// ffmpeg backpressure stall. All assertions must complete while
// WriteFrame is still blocked - that is the whole point.

import (
	"os"
	"sync"
	"testing"
	"time"
)

// fillPipe blocks the writer end until the reader drains: fills the
// kernel pipe buffer (64KB default) plus a userspace os.Pipe-side copy
// stash, then performs one oversized write that cannot proceed.
// It returns once the write is parked, and unblocks when the reader
// consumes (via the returned drain func).
func fillPipeBlockingWrite(t *testing.T, w *os.File, r *os.File) (parked <-chan struct{}, drain func()) {
	written := make(chan struct{}, 1)
	go func() {
		// 8MB exceeds every buffer layer; the write parks.
		buf := make([]byte, 8<<20)
		_, _ = w.Write(buf)
		written <- struct{}{}
	}()
	// Parked = the write did not finish promptly.
	select {
	case <-written:
		t.Fatal("8MB write unexpectedly completed into a 64KB pipe")
	case <-time.After(50 * time.Millisecond):
	}
	return written, func() {
		// Continuously drain until the parked writer finishes.
		go func() {
			buf := make([]byte, 1<<20)
			for {
				if _, err := r.Read(buf); err != nil {
					return
				}
				select {
				case <-written:
					return
				default:
				}
			}
		}()
	}
}

func TestIssue4352WriteFrameDoesNotHoldLockWhileBlocked(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	e := &Encoder{running: true, stdin: pw}

	parked, drain := fillPipeBlockingWrite(t, pw, pr)
	_ = parked

	// Consume the lock on the main test path: an 8MB WriteFrame blocks
	// (pipe is full because fillPipeBlockingWrite's write already parked
	// the buffer - two writers on one full pipe both block).
	frameBlocked := make(chan error, 1)
	go func() { frameBlocked <- e.WriteFrame(make([]byte, 8<<20)) }()

	// Core assertion: while WriteFrame is blocked on the full pipe, other
	// e.mu users must complete. Give the goroutine time to enter the
	// (formerly lock-held) write first.
	time.Sleep(100 * time.Millisecond)

	lockWait := make(chan struct{})
	go func() {
		_ = e.IsRunning() // takes e.mu
		close(lockWait)
	}()
	select {
	case <-lockWait:
		// Lock is free while WriteFrame is parked - fix works.
	case <-time.After(2 * time.Second):
		t.Fatal("#4352 regression: IsRunning still blocked while WriteFrame is parked in the stdin write - WriteFrame must not hold e.mu across the write")
	}

	// Cleanup: closing the writer end unparks both blocked writers with
	// EPIPE (cleanup-only; correctness was asserted above). Reader close
	// follows so the parked fillPipe goroutine also exits.
	pw.Close()
	pr.Close()
	select {
	case <-frameBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("WriteFrame never returned after pipe close")
	}
	_ = drain
}

func TestIssue4352StopUnblocksDuringParkedWrite(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	e := &Encoder{running: true, stdin: pw}

	var once sync.Once
	exit := make(chan struct{})
	e.exitCh = exit
	e.cmd = nil // Kill path is nil-safe; graceful path uses exitCh below.
	go func() {
		once.Do(func() { close(exit) })
	}()

	frameBlocked := make(chan error, 1)
	// Park a writer first so the pipe is full (like ffmpeg backpressure).
	_, _ = pw.Write(make([]byte, 64<<10)) // fill kernel buffer
	go func() { frameBlocked <- e.WriteFrame(make([]byte, 4<<20)) }()
	time.Sleep(100 * time.Millisecond)

	// Stop must acquire the lock, flip state and close stdin even while
	// WriteFrame is parked. Bounded: exitCh is closed immediately, so the
	// graceful branch returns at once.
	done := make(chan struct{})
	go func() { _ = e.Stop(); close(done) }()
	select {
	case <-done:
		// Stop completed while a frame write was parked - escape hatch intact.
	case <-time.After(3 * time.Second):
		t.Fatal("#4352 regression: Stop blocked behind a parked WriteFrame")
	}

	// After Stop, WriteFrame must report not-running promptly (lock free).
	errCh := make(chan error, 1)
	go func() { errCh <- e.WriteFrame([]byte("x")) }()
	select {
	case err := <-errCh:
		if err == nil || err.Error() != "encoder not running" {
			t.Fatalf("post-Stop WriteFrame = %v, want encoder not running", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-Stop WriteFrame blocked - lock still wedged")
	}
}
