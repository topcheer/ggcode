package im

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestDaemonBridgeInterruptionQueuing verifies that when an agent run is
// active (cancelFunc != nil), a new IM message is queued as an interruption
// instead of cancelling the current run.
func TestDaemonBridgeInterruptionQueuing(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate an active agent run
	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	// Send a message while agent is running
	err := bridge.SubmitInboundMessage(ctx, InboundMessage{
		Text:     "second message",
		Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage during active run: %v", err)
	}

	// Verify the message was queued
	bridge.mu.Lock()
	pending := bridge.pendingInterruptions
	bridge.mu.Unlock()
	if len(pending) != 1 || pending[0] != "second message" {
		t.Fatalf("expected 1 pending interruption, got %v", pending)
	}

	// CRITICAL: context must NOT be cancelled — old code would cancel here
	if ctx.Err() != nil {
		t.Fatal("BUG: context was cancelled — new messages must NOT cancel active agent run")
	}
}

// TestDaemonBridgeInterruptionQueueOrder verifies messages are queued in order.
func TestDaemonBridgeInterruptionQueueOrder(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	for _, text := range []string{"msg1", "msg2", "msg3"} {
		_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
			Text:     text,
			Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
		})
	}

	bridge.mu.Lock()
	pending := bridge.pendingInterruptions
	bridge.mu.Unlock()
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(pending))
	}
	if pending[0] != "msg1" || pending[1] != "msg2" || pending[2] != "msg3" {
		t.Fatalf("wrong order: %v", pending)
	}
}

// TestDaemonBridgeInterruptionDrain verifies the handler drains the queue correctly.
func TestDaemonBridgeInterruptionDrain(t *testing.T) {
	bridge := &DaemonBridge{
		pendingInterruptions: []string{"msg1", "msg2"},
	}

	// This mirrors the handler set up in SubmitInboundMessage
	handler := func() string {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		if len(bridge.pendingInterruptions) == 0 {
			return ""
		}
		msg := bridge.pendingInterruptions[0]
		bridge.pendingInterruptions = bridge.pendingInterruptions[1:]
		return msg
	}

	msg1 := handler()
	msg2 := handler()
	msg3 := handler()

	if msg1 != "msg1" {
		t.Fatalf("expected msg1, got %q", msg1)
	}
	if msg2 != "msg2" {
		t.Fatalf("expected msg2, got %q", msg2)
	}
	if msg3 != "" {
		t.Fatalf("expected empty, got %q", msg3)
	}
}

// TestDaemonBridgeEmptyMessageIgnored verifies empty messages are not queued.
func TestDaemonBridgeEmptyMessageIgnored(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
		Text:     "",
		Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
	})

	bridge.mu.Lock()
	pending := bridge.pendingInterruptions
	bridge.mu.Unlock()
	if len(pending) != 0 {
		t.Fatalf("empty messages should not be queued, got %v", pending)
	}
}

// TestDaemonBridgeNoRaceOnInterruption tests concurrent access to the queue.
func TestDaemonBridgeNoRaceOnInterruption(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
				Text:     "concurrent msg",
				Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
			})
		}()
	}
	wg.Wait()

	bridge.mu.Lock()
	count := len(bridge.pendingInterruptions)
	bridge.mu.Unlock()
	if count != 10 {
		t.Fatalf("expected 10 queued messages, got %d", count)
	}
}

// TestDaemonBridgeNoCancelStress repeats the no-cancel check under rapid messages.
func TestDaemonBridgeNoCancelStress(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	for i := 0; i < 100; i++ {
		_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
			Text:     "msg",
			Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
		})
	}

	time.Sleep(10 * time.Millisecond)

	if ctx.Err() != nil {
		t.Fatal("BUG: context was cancelled after 100 rapid messages")
	}

	bridge.mu.Lock()
	count := len(bridge.pendingInterruptions)
	bridge.mu.Unlock()
	if count != 100 {
		t.Fatalf("expected 100 queued, got %d", count)
	}
}
