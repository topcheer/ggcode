package stream

// Regression tests for the #1292 follow-ups:
// - #1294: Encoder.Read raced the monitor's e.stdout = nil (data race +
//   nil-deref panic in the broadcaster -> silent dead stream). Read now
//   snapshots stdout under the lock and returns EOF once reaped.
// - #1293: Manager.Stop closed stopCh before stopping the encoder, so the
//   broadcaster (sole stdout reader) exited before ffmpeg could flush,
//   defeating #1292's graceful window. Encoder stop now precedes the
//   stopCh close. (Manager-level ordering is exercised via the broadcaster
//   contract below: after Encoder.Stop, Read returns EOF cleanly.)

import (
	"sync"
	"testing"
	"time"
)

// After the encoder has been stopped and reaped, Read must return EOF
// (never panic on a nil stdout, never block).
func TestIssue1294_ReadAfterStopReturnsEOF(t *testing.T) {
	fakeFFmpegDir(t, "cat > /dev/null\nexit 0\n")
	enc := NewEncoder(4, 4, 26, 1, "")
	if err := enc.Start(); err != nil {
		t.Skipf("fake ffmpeg start failed: %v", err)
	}
	if err := enc.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	done := make(chan struct{})
	var gotErr error
	go func() {
		defer close(done)
		_, gotErr = enc.Read(make([]byte, 64))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Read blocked after the encoder was reaped")
	}
	if gotErr == nil {
		t.Fatal("Read after reaping must return an error (EOF), got nil")
	}
}

// Concurrent Read during a crash/stop must be race-clean and panic-free
// (run under -race; the old code was a direct e.stdout.Read on a field the
// monitor nils out under the lock).
func TestIssue1294_ConcurrentReadDuringCrash(t *testing.T) {
	fakeFFmpegDir(t, "sleep 0.05\ncat > /dev/null\nexit 1\n")
	enc := NewEncoder(4, 4, 26, 1, "")
	if err := enc.Start(); err != nil {
		t.Skipf("fake ffmpeg start failed: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 64)
			for {
				_, err := enc.Read(buf)
				if err != nil {
					return // EOF/closed: expected terminal state
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(80 * time.Millisecond) // let readers get into Read
		_ = enc.Stop()
	}()
	wg.Wait()

	// Post-stop contract for #1293: once Stop has reaped the encoder, a
	// reader sees EOF immediately - the broadcaster drains any remaining
	// flush data and terminates on its own without needing stopCh.
	if _, err := enc.Read(make([]byte, 64)); err == nil {
		t.Fatal("Read after Stop must return EOF so the broadcaster exits on its own")
	}
}
