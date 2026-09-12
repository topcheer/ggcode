package acp

// #2109 follow-up: the outbound writer goroutine parked forever on the
// outbox channel - outbox was never closed, and each Transport owns one
// ACP agent connection (client.go spawns one per agent), so every spawned
// agent leaked one goroutine plus its queue. CloseWriter must stop the
// writer (drain-then-exit), and late enqueues must fail cleanly instead of
// panicking on a closed channel.

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCloseWriterStopsWriterGoroutine(t *testing.T) {
	tr := NewTransport(strings.NewReader(""), &bytes.Buffer{})
	tr.writeDroppable([]byte(`{"a":1}`)) // ensure the writer is running
	tr.CloseWriter()

	select {
	case <-tr.writerDone:
		// writer drained and exited
	case <-time.After(2 * time.Second):
		t.Fatal("writer goroutine must exit after CloseWriter")
	}
}

func TestWriteAfterStopFailsCleanly(t *testing.T) {
	tr := NewTransport(strings.NewReader(""), &bytes.Buffer{})
	tr.CloseWriter()
	if err := tr.writeBlocking([]byte(`{"b":2}`)); err == nil {
		t.Fatal("writeBlocking after stop must fail, not panic or block")
	}
	// Droppable must neither panic nor block - it silently counts.
	tr.writeDroppable([]byte(`{"c":3}`))
	if tr.dropped.Load() == 0 {
		t.Fatal("post-stop droppable write must count as dropped")
	}
}

func TestStopWriterRacesEnqueues(t *testing.T) {
	// Concurrent stopWriter vs writeBlocking/writeDroppable from several
	// goroutines: the sendMu/stopped guard must prevent both the closed-
	// channel panic and unbounded blocking.
	tr := NewTransport(strings.NewReader(""), &bytes.Buffer{})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tr.writeDroppable([]byte(`{"x":1}`))
				_ = tr.writeBlocking([]byte(`{"y":2}`))
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	tr.CloseWriter()
	close(stop)
	wg.Wait()
	select {
	case <-tr.writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("writer must exit after concurrent stop")
	}
}
