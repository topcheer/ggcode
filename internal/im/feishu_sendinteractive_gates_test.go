package im

import (
	"context"
	"strings"
	"testing"
)

// Offline behavior pins for feishuAdapter.SendInteractive gates (r211).
// Written BEFORE the SendInteractive split and locked here: gate order
// (connection -> channel) and error text must stay byte-identical through
// the refactor. No network is touched: both pins return at the gates.
func TestFeishuSendInteractiveGate_NotConnected(t *testing.T) {
	a := &feishuAdapter{name: "t"}
	_, err := a.SendInteractive(context.Background(), ChannelBinding{ChannelID: "ch-1"}, InteractiveMessage{Text: "hi"})
	if err == nil {
		t.Fatal("SendInteractive on offline adapter must fail")
	}
	if got, want := err.Error(), `Feishu bot "t" is not online`; got != want {
		t.Fatalf("not-connected gate error = %q, want %q", got, want)
	}
}

func TestFeishuSendInteractiveGate_EmptyChannel(t *testing.T) {
	a := &feishuAdapter{name: "t", connected: true}
	// Whitespace-only channel id must hit the same empty-channel gate.
	_, err := a.SendInteractive(context.Background(), ChannelBinding{ChannelID: "   "}, InteractiveMessage{Text: "hi"})
	if err == nil {
		t.Fatal("SendInteractive with empty channel must fail")
	}
	if got, want := err.Error(), "Feishu channel is not configured"; got != want {
		t.Fatalf("empty-channel gate error = %q, want %q", got, want)
	}
	if !strings.Contains(err.Error(), "Feishu") {
		t.Fatalf("error must name platform: %q", err.Error())
	}
}
