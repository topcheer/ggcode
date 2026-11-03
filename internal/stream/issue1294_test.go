package stream

import (
	"sync"
	"testing"
	"time"
)

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
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(80 * time.Millisecond)
		_ = enc.Stop()
	}()
	wg.Wait()

	if _, err := enc.Read(make([]byte, 64)); err == nil {
		t.Fatal("Read after Stop must return EOF so the broadcaster exits on its own")
	}
}
