package acp

// #2109 regression: writeJSON held t.mu across a blocking Write - when the
// editor side stopped reading and the pipe filled, SendRequest froze on
// t.mu.Lock() (never reaching its own select), the main loop froze in
// handleStreamEvent, and Stop() could not help (write(2) ignores ctx).
// The outbound queue + single writer goroutine keeps the mutex off the
// write path: notifications drop when the queue is full, requests fail
// with a bounded error instead of freezing the process.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// stuckWriter blocks every Write until released.
type stuckWriter struct{ release chan struct{} }

func (w *stuckWriter) Write(p []byte) (int, error) {
	<-w.release
	return len(p), nil
}

func TestNotificationFloodDoesNotFreezeSendRequest(t *testing.T) {
	w := &stuckWriter{release: make(chan struct{})}
	tr := NewTransport(strings.NewReader(""), w) // reader irrelevant here

	// Flood far past the queue capacity - every call must return
	// promptly (drops after the queue fills), never block on write(2).
	floodDone := make(chan struct{})
	go func() {
		defer close(floodDone)
		for i := 0; i < outboundQueueCap*4; i++ {
			if err := tr.WriteNotification("session/update", map[string]any{"i": i}); err != nil {
				t.Errorf("notification must never fail: %v", err)
				return
			}
		}
	}()
	select {
	case <-floodDone:
	case <-time.After(5 * time.Second):
		t.Fatal("notification flood is blocked - the writer goroutine freeze is back")
	}

	// A SendRequest under the same stuck writer must fail with a bounded
	// error (queue full / write stalled), NOT hang on the mutex past its
	// own deadline.
	reqDone := make(chan error, 1)
	go func() {
		_, err := tr.SendRequest(context.Background(), "session/request_permission", nil, 200*time.Millisecond)
		reqDone <- err
	}()
	select {
	case err := <-reqDone:
		if err == nil {
			t.Fatal("SendRequest under a stuck writer must fail, not succeed")
		}
	case <-time.After(outboundDeadline + 2*time.Second):
		t.Fatal("SendRequest froze past the outbound deadline - mutex-held write is back")
	}

	// Release the writer so the test goroutine's deferred writes drain.
	close(w.release)
}
