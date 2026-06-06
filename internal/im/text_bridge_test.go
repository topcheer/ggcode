package im

import (
	"context"
	"testing"
)

func TestTextSubmitBridgeBuildsInboundText(t *testing.T) {
	var got string
	bridge := &TextSubmitBridge{
		Submit: func(_ context.Context, text string) error {
			got = text
			return nil
		},
	}
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage returned error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

func TestTextSubmitBridgeSkipsEmptyInboundText(t *testing.T) {
	called := false
	bridge := &TextSubmitBridge{
		Submit: func(_ context.Context, text string) error {
			called = true
			return nil
		},
	}
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{})
	if err != nil {
		t.Fatalf("SubmitInboundMessage returned error: %v", err)
	}
	if called {
		t.Fatal("expected Submit not to be called for empty inbound text")
	}
}
