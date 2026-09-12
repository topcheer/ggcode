package acp

// #2147 regression: the outbound writer goroutine closed the ack
// unconditionally after a failed Write - ack meant "dequeued", not
// "written", so EPIPE etc. were swallowed: WriteResponse returned nil on
// a failed write and SendRequest waited out its full response timeout
// for a request that never reached the wire.
//
// #2148 P1: writeBlocking parked for queue space while HOLDING sendMu,
// freezing the droppable notification path (the per-token stream) for
// up to 10s per parked write. P2: CloseWriter closed the underlying
// writer without waiting for the announced drain, silently losing every
// still-queued message.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// failingWriter fails every Write.
type failingWriter struct{}

func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("broken pipe")
}

// #2147: a blocking-class write whose underlying Write fails must return
// the error to the caller (was: nil - silently "sent").
func TestBlockingWritePropagatesWriteError(t *testing.T) {
	tr := NewTransport(strings.NewReader(""), &failingWriter{})
	if err := tr.WriteResponse(1, "ok"); err == nil {
		t.Fatal("WriteResponse must surface the underlying write error (was: swallowed, nil)")
	}
}

// slowWriter stalls each Write briefly.
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

// #2148 P1: while a blocking write is queued behind a FULL queue, a
// droppable notification must still return near-instantly (drop), not
// freeze on sendMu behind the parked blocking write.
func TestDroppableNotFrozenByParkedBlockingWrite(t *testing.T) {
	w := &slowWriter{delay: 40 * time.Millisecond}
	tr := NewTransport(strings.NewReader(""), w)

	// One blocking write holds the single writer busy; fill the queue
	// with droppables until full, then park a second blocking write.
	_ = tr.writeBlocking([]byte(`{"x":1}`)) // returns after write
	go func() { _ = tr.writeBlocking([]byte(`{"y":1}`)) }()
	for i := 0; i < outboundQueueCap; i++ {
		tr.writeDroppable([]byte(`{"n":1}`))
	}
	time.Sleep(20 * time.Millisecond) // let the second blocking write park

	begin := time.Now()
	tr.writeDroppable([]byte(`{"z":1}`))
	if d := time.Since(begin); d > 500*time.Millisecond {
		t.Fatalf("droppable froze %v behind a parked blocking write (sendMu held during park is back)", d)
	}
}

// #2148 P2: CloseWriter must wait for the drain - queued messages get a
// chance to be written before the underlying writer closes.
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
	if n < queued-2 { // allow small scheduling slack, not 31/32-loss
		t.Fatalf("drain-then-exit broken: only %d/%d writes landed before close", n, queued)
	}
}
