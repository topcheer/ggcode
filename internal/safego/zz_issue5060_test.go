package safego

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns whatever
// was written to stderr during that window.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan struct{})
	var buf strings.Builder
	var mu sync.Mutex
	go func() {
		defer close(done)
		b, _ := io.ReadAll(r)
		mu.Lock()
		buf.Write(b)
		mu.Unlock()
	}()

	fn()

	w.Close()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

// #5060 pin: a recovered panic must ALWAYS be visible on stderr, even when
// no logger and no panic hook are wired (the silent-swallow path).
func TestPanicAlwaysVisibleOnStderrWithoutLogger(t *testing.T) {
	savedLog := logFn
	logFn = nil
	defer func() { logFn = savedLog }()

	out := captureStderr(t, func() {
		done := make(chan struct{})
		Go("silent-goroutine", func() {
			defer close(done)
			panic("boom-5060")
		})
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("goroutine did not finish")
		}
		// Give the deferred Recover a beat to flush its stderr write.
		time.Sleep(50 * time.Millisecond)
	})

	if !strings.Contains(out, "PANIC in goroutine") || !strings.Contains(out, "silent-goroutine") || !strings.Contains(out, "boom-5060") {
		t.Fatalf("stderr missing panic trace (silent swallow): %q", out)
	}
	if !strings.Contains(out, "[safego]") {
		t.Fatalf("stderr missing source tag [safego]: %q", out)
	}
}

// Pin: stderr mirroring also works through Run (synchronous path).
func TestRunPanicVisibleOnStderr(t *testing.T) {
	savedLog := logFn
	logFn = nil
	defer func() { logFn = savedLog }()

	out := captureStderr(t, func() {
		Run("sync-goroutine", func() { panic("run-boom") })
	})

	if !strings.Contains(out, "PANIC in goroutine") || !strings.Contains(out, "run-boom") {
		t.Fatalf("Run panic not visible on stderr: %q", out)
	}
}
