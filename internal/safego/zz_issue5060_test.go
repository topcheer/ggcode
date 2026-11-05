package safego

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureStderrSink swaps the package-level stderrSink for a synchronized
// buffer, runs fn, and returns what was written. Production code always
// writes to os.Stderr; only tests redirect via this sink.
func captureStderrSink(t *testing.T, fn func()) string {
	t.Helper()
	var mu sync.Mutex
	var buf strings.Builder
	old := stderrSink.Load()
	w := io.Writer(syncWriter{&mu, &buf})
	stderrSink.Store(&w)
	defer func() { stderrSink.Store(old) }()

	fn()

	// Give async goroutines a beat to flush their writes.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

type syncWriter struct {
	mu *sync.Mutex
	b  *strings.Builder
}

func (w syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// #5060 pin: a recovered panic must ALWAYS be visible on the stderr sink,
// even when no logger and no panic hook are wired (the silent-swallow path).
func TestPanicAlwaysVisibleOnStderrWithoutLogger(t *testing.T) {
	// Deliberately unwire the logger: the exact silent-swallow path.
	var saved func(category, format string, args ...any)
	if l := logger(); l != nil {
		saved = l
	}
	SetLogger(nil)
	defer SetLogger(saved)

	out := captureStderrSink(t, func() {
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
	var saved func(category, format string, args ...any)
	if l := logger(); l != nil {
		saved = l
	}
	SetLogger(nil)
	defer SetLogger(saved)

	out := captureStderrSink(t, func() {
		Run("sync-goroutine", func() { panic("run-boom") })
	})

	if !strings.Contains(out, "PANIC in goroutine") || !strings.Contains(out, "run-boom") {
		t.Fatalf("Run panic not visible on stderr: %q", out)
	}
}
