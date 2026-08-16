package main

// Issue #530 characteristic tests:
//   - Bug A: DeleteSession nil chat (onboarding path) must not panic
//   - Bug C: StartShare refresh failure must tear down stale tunnel session/broker
//   - Bug D: RespondAskUser invalid payload must cancel the waiter + emit error

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// TestDeleteSessionNilChatNoPanic verifies the #530 Bug A fix: a.chat can be
// nil via the startup onboarding early-return path or NewChatBridge failure,
// and the unconditional chat.MarkSessionDeleted(id) locked a nil receiver's
// mutex and panicked the whole process.
func TestDeleteSessionNilChatNoPanic(t *testing.T) {
	app := &App{} // chat == nil, exactly the onboarding state

	// Must not panic; a store-level error for a nonexistent ID is fine.
	if err := app.DeleteSession("zz-530-nonexistent-session"); err != nil {
		t.Logf("DeleteSession returned error for nonexistent session (acceptable): %v", err)
	}
}

// TestStartShareRefreshFailureClearsStaleTunnelState verifies the #530 Bug C
// fix: when refreshing an existing share's invite fails (relay restart / dead
// room), the old tunnel session and broker must be fully torn down before a
// fresh share is created. Pre-fix, only tunnelSession was nil-ed — the old
// session was never stopped and the dead broker stayed attached.
func TestStartShareRefreshFailureClearsStaleTunnelState(t *testing.T) {
	app := &App{} // chat == nil so StartShare fails after the refresh path

	// Stale session: a zero-value Session has no relay URL, so
	// RefreshInvite fails — simulating a share whose room is no longer live.
	// NewBroker(nil) yields a fully-initialized broker (test-safe teardown)
	// with no attached session.
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

// TestRespondAskUserInvalidPayloadCancelsWaiter verifies the #530 Bug D fix:
// a malformed answers payload used to be silently dropped, leaving the
// pending ask_user waiter blocked forever (RequestAskUser waits on
// context.WithoutCancel with no timeout). The fix must deliver a cancelled
// response to unblock the agent run and surface an error event.
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

	// Wait until the waiter is registered in the interaction broker.
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

	// The failure must also be surfaced to the frontend as an error event.
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
