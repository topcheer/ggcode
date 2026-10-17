package provider

import (
	"context"
	"testing"
	"time"
)

// #4266: the SSE send helper must prefer ctx cancellation over an
// indefinite block on a full channel - the pump goroutine's exit path.
func TestIssue4266_SendEventHonorsCancelOverFullChannel(t *testing.T) {
	ch := make(chan StreamEvent) // unbuffered = permanently full (no reader)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if sendResponsesEvent(ctx, ch, StreamEvent{Type: StreamEventText, Text: "x"}) {
		t.Fatal("send must fail on cancelled ctx with a full channel")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("send blocked %v - ctx.Done must win, not the channel send", elapsed)
	}
	// Non-cancelled path delivers.
	ctx2 := context.Background()
	ch2 := make(chan StreamEvent, 1)
	if !sendResponsesEvent(ctx2, ch2, StreamEvent{Type: StreamEventText}) {
		t.Fatal("healthy path must deliver")
	}
}
