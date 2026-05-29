package metrics

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCollector_DeliversEvents(t *testing.T) {
	var mu sync.Mutex
	var received []MetricEvent

	c := NewCollector(context.Background(), 16, func(m MetricEvent) {
		mu.Lock()
		received = append(received, m)
		mu.Unlock()
	})
	defer c.Stop()

	c.Emit(MetricEvent{Type: "llm", TTFT: 100 * time.Millisecond})
	c.Emit(MetricEvent{Type: "tool", ToolName: "read_file", ToolSuccess: true})

	// Give the goroutine time to drain
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
	if received[0].Type != "llm" {
		t.Errorf("event 0 type: got %q, want llm", received[0].Type)
	}
	if received[1].Type != "tool" {
		t.Errorf("event 1 type: got %q, want tool", received[1].Type)
	}
}

func TestCollector_DropsWhenFull(t *testing.T) {
	block := make(chan struct{})
	c := NewCollector(context.Background(), 2, func(m MetricEvent) {
		<-block // block to fill the channel
	})

	// Fill the buffer
	c.Emit(MetricEvent{Type: "llm"})
	c.Emit(MetricEvent{Type: "llm"})

	// This should not block
	done := make(chan struct{})
	go func() {
		c.Emit(MetricEvent{Type: "tool"}) // should be dropped
		close(done)
	}()

	select {
	case <-done:
		// good — Emit didn't block
	case <-time.After(time.Second):
		t.Fatal("Emit blocked when channel was full")
	}

	close(block)
	c.Stop()
}

func TestCollector_StopDrainsRemaining(t *testing.T) {
	var mu sync.Mutex
	var received []MetricEvent

	c := NewCollector(context.Background(), 16, func(m MetricEvent) {
		mu.Lock()
		received = append(received, m)
		mu.Unlock()
	})

	c.Emit(MetricEvent{Type: "llm"})
	c.Emit(MetricEvent{Type: "tool"})
	c.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 events after stop, got %d", len(received))
	}
}

func TestCollector_ContextCancelDrainsRemaining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var received []MetricEvent
	done := make(chan struct{})
	c := NewCollector(ctx, 16, func(m MetricEvent) {
		mu.Lock()
		received = append(received, m)
		count := len(received)
		mu.Unlock()
		if count == 2 {
			close(done)
		}
	})

	c.Emit(MetricEvent{Type: "llm"})
	c.Emit(MetricEvent{Type: "tool"})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector did not drain after context cancellation")
	}
}
