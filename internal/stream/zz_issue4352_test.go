package stream

import (
	"os"
	"sync"
	"testing"
	"time"
)

func fillPipeBlockingWrite(t *testing.T, w *os.File, r *os.File) (parked <-chan struct{}, drain func()) {
	written := make(chan struct{}, 1)
	go func() {

		buf := make([]byte, 8<<20)
		_, _ = w.Write(buf)
		written <- struct{}{}
	}()

	select {
	case <-written:
		t.Fatal("8MB write unexpectedly completed into a 64KB pipe")
	case <-time.After(50 * time.Millisecond):
	}
	return written, func() {

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

	frameBlocked := make(chan error, 1)
	go func() { frameBlocked <- e.WriteFrame(make([]byte, 8<<20)) }()

	time.Sleep(100 * time.Millisecond)

	lockWait := make(chan struct{})
	go func() {
		_ = e.IsRunning()
		close(lockWait)
	}()
	select {
	case <-lockWait:

	case <-time.After(2 * time.Second):
		t.Fatal("#4352 regression: IsRunning still blocked while WriteFrame is parked in the stdin write - WriteFrame must not hold e.mu across the write")
	}

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
	e.cmd = nil
	go func() {
		once.Do(func() { close(exit) })
	}()

	frameBlocked := make(chan error, 1)

	_, _ = pw.Write(make([]byte, 64<<10))
	go func() { frameBlocked <- e.WriteFrame(make([]byte, 4<<20)) }()
	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{})
	go func() { _ = e.Stop(); close(done) }()
	select {
	case <-done:

	case <-time.After(3 * time.Second):
		t.Fatal("#4352 regression: Stop blocked behind a parked WriteFrame")
	}

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
