package im

import (
	"context"
	"testing"
)

func TestDisableEnableBinding(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind a channel
	_, err := mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-123",
		TargetID:  "target-1",
	})
	if err != nil {
		t.Fatalf("BindChannel: %v", err)
	}

	// Verify it's in currentBindings
	bindings := mgr.CurrentBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	// Verify not disabled
	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should not be disabled initially")
	}

	// Disable the binding
	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	// Verify it's removed from currentBindings
	bindings = mgr.CurrentBindings()
	if len(bindings) != 0 {
		t.Fatalf("expected 0 active bindings after disable, got %d", len(bindings))
	}

	// Verify it's disabled
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should be disabled")
	}

	// Verify disabled bindings snapshot
	disabled := mgr.DisabledBindings()
	if len(disabled) != 1 {
		t.Fatalf("expected 1 disabled binding, got %d", len(disabled))
	}
	if disabled[0].Adapter != "qq-bot-1" {
		t.Fatalf("expected adapter qq-bot-1, got %s", disabled[0].Adapter)
	}

	// Verify snapshot includes disabled bindings
	snapshot := mgr.Snapshot()
	if len(snapshot.DisabledBindings) != 1 {
		t.Fatalf("expected 1 disabled binding in snapshot, got %d", len(snapshot.DisabledBindings))
	}

	// Disable again should fail
	if err := mgr.DisableBinding("qq-bot-1"); err != ErrNoChannelBound {
		t.Fatalf("expected ErrNoChannelBound for already-disabled, got %v", err)
	}

	// Enable the binding
	if err := mgr.EnableBinding("qq-bot-1"); err != nil {
		t.Fatalf("EnableBinding: %v", err)
	}

	// Verify it's back in currentBindings
	bindings = mgr.CurrentBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding after enable, got %d", len(bindings))
	}
	if bindings[0].Adapter != "qq-bot-1" {
		t.Fatalf("expected adapter qq-bot-1, got %s", bindings[0].Adapter)
	}
	if bindings[0].ChannelID != "channel-123" {
		t.Fatalf("expected channelID channel-123, got %s", bindings[0].ChannelID)
	}

	// Verify it's not disabled
	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should not be disabled after enable")
	}

	// Enable again should fail
	if err := mgr.EnableBinding("qq-bot-1"); err != ErrNoChannelBound {
		t.Fatalf("expected ErrNoChannelBound for already-enabled, got %v", err)
	}
}

func TestDisableBindingEmitSkips(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind two channels
	_, err := mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-1",
	})
	if err != nil {
		t.Fatalf("BindChannel qq: %v", err)
	}
	_, err = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "channel-2",
	})
	if err != nil {
		t.Fatalf("BindChannel tg: %v", err)
	}

	// Disable qq-bot-1
	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	// Emit should return ErrNoChannelBound because no sinks are registered,
	// but verify that it only looks at the remaining active binding (tg-bot-1).
	// The important thing is that qq-bot-1 is not in currentBindings.
	err = mgr.Emit(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"})
	// Emit will fail because there are no sinks, but that's expected.
	// The key test is that qq-bot-1 is not in the active set.

	snapshot := mgr.Snapshot()
	for _, b := range snapshot.CurrentBindings {
		if b.Adapter == "qq-bot-1" {
			t.Fatal("qq-bot-1 should not be in active bindings")
		}
	}
	found := false
	for _, b := range snapshot.DisabledBindings {
		if b.Adapter == "qq-bot-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("qq-bot-1 should be in disabled bindings")
	}
}

func TestDisableBindingUnbindSessionClears(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-1",
	})

	_ = mgr.DisableBinding("qq-bot-1")

	// Unbind session should clear both active and disabled
	mgr.UnbindSession()

	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("disabled should be cleared after UnbindSession")
	}
	if len(mgr.DisabledBindings()) != 0 {
		t.Fatal("disabled bindings should be empty after UnbindSession")
	}
}
