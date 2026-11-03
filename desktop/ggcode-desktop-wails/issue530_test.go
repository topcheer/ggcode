package main

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestDeleteSessionNilChatNoPanic(t *testing.T) {
	app := &App{}

	if err := app.DeleteSession("zz-530-nonexistent-session"); err != nil {
		t.Logf("DeleteSession returned error for nonexistent session (acceptable): %v", err)
	}
}

func TestStartShareRefreshFailureClearsStaleTunnelState(t *testing.T) {

	t.Setenv("HOME", t.TempDir())
	app := &App{}

	app.setTunnelState(&tunnel.Session{}, tunnel.NewBroker(nil))
	if app.currentTunnelSession() == nil || app.currentTunnelBroker() == nil {
		t.Fatal("setup: expected stale tunnel session and broker to be set")
	}

	_, err := app.StartShare()
	if err == nil {
		t.Fatal("expected StartShare to fail with nil chat (after refresh path)")
	}

	if app.currentTunnelSession() != nil {
		t.Fatal("stale tunnel session leaked after refresh failure")
	}
	if app.currentTunnelBroker() != nil {
		t.Fatal("stale tunnel broker leaked after refresh failure")
	}
}

func TestRespondAskUserInvalidPayloadCancelsWaiter(t *testing.T) {
	chat, err := wailskit.NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	app := &App{
		ctx:          context.Background(),
		streamEvents: make(chan uiEvent, 4),
		chat:         chat,
	}

	req := tool.AskUserRequest{
		Questions: []tool.AskUserQuestion{{
			ID:     "q1",
			Kind:   tool.AskUserKindText,
			Prompt: "name?",
		}},
	}
	done := make(chan tool.AskUserResponse, 1)
	go func() {
		resp, _ := chat.RequestAskUser(context.Background(), "req-530", req)
		done <- resp
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, _, ok := chat.PendingAskUserRequest(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ask_user request never became pending")
		}
		time.Sleep(5 * time.Millisecond)
	}

	app.RespondAskUser("req-530", "{invalid-json")

	select {
	case resp := <-done:
		if resp.Status != tool.AskUserStatusCancelled {
			t.Fatalf("expected cancelled response for invalid payload, got %q", resp.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask_user waiter hung after invalid payload — agent run would be stranded")
	}

	select {
	case ev := <-app.streamEvents:
		if ev.name != "chat:stream" {
			t.Fatalf("expected chat:stream UI event, got %q", ev.name)
		}
		payload, ok := ev.payload.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map payload, got %T", ev.payload)
		}
		if payload["type"] != "error" {
			t.Fatalf("expected payload type error, got %#v", payload["type"])
		}
	default:
		t.Fatal("expected error stream event to be queued for invalid payload")
	}
}
