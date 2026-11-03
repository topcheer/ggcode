package acp

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type failingWriter struct{}

func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("broken pipe")
}

func TestBlockingWritePropagatesWriteError(t *testing.T) {
	tr := NewTransport(strings.NewReader(""), &failingWriter{})
	if err := tr.WriteResponse(1, "ok"); err == nil {
		t.Fatal("WriteResponse must surface the underlying write error (was: swallowed, nil)")
	}
}

type slowWriter struct {
	delay time.Duration
	mu    sync.Mutex
	n     int
}

func (w *slowWriter) Write(p []byte) (int, error) {
	time.Sleep(w.delay)
	w.mu.Lock()
	w.n++
	w.mu.Unlock()
	return len(p), nil
}

func TestDroppableNotFrozenByParkedBlockingWrite(t *testing.T) {
	w := &slowWriter{delay: 40 * time.Millisecond}
	tr := NewTransport(strings.NewReader(""), w)

	_ = tr.writeBlocking([]byte(`{"x":1}`))
	go func() { _ = tr.writeBlocking([]byte(`{"y":1}`)) }()
	for i := 0; i < outboundQueueCap; i++ {
		tr.writeDroppable([]byte(`{"n":1}`))
	}
	time.Sleep(20 * time.Millisecond)

	begin := time.Now()
	tr.writeDroppable([]byte(`{"z":1}`))
	if d := time.Since(begin); d > 500*time.Millisecond {
		t.Fatalf("droppable froze %v behind a parked blocking write (sendMu held during park is back)", d)
	}
}

func TestCloseWriterDrainsQueuedMessages(t *testing.T) {
	w := &slowWriter{delay: 2 * time.Millisecond}
	tr := NewTransport(strings.NewReader(""), w)

	const queued = 20
	for i := 0; i < queued; i++ {
		tr.writeDroppable([]byte(`{"m":1}`))
	}
	if err := tr.CloseWriter(); err != nil {
		t.Fatalf("CloseWriter: %v", err)
	}
	w.mu.Lock()
	n := w.n
	w.mu.Unlock()
	if n < queued-2 {
		t.Fatalf("drain-then-exit broken: only %d/%d writes landed before close", n, queued)
	}
}
