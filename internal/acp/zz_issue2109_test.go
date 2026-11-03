package acp

import (
	"context"
	"strings"
	"testing"
	"time"
)

type stuckWriter struct{ release chan struct{} }

func (w *stuckWriter) Write(p []byte) (int, error) {
	<-w.release
	return len(p), nil
}

func TestNotificationFloodDoesNotFreezeSendRequest(t *testing.T) {
	w := &stuckWriter{release: make(chan struct{})}
	tr := NewTransport(strings.NewReader(""), w)

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

	close(w.release)
}
