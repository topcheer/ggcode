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

	bindings := mgr.CurrentBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should not be disabled initially")
	}

	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	bindings = mgr.CurrentBindings()
	if len(bindings) != 0 {
		t.Fatalf("expected 0 active bindings after disable, got %d", len(bindings))
	}

	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should be disabled")
	}

	disabled := mgr.DisabledBindings()
	if len(disabled) != 1 {
		t.Fatalf("expected 1 disabled binding, got %d", len(disabled))
	}
	if disabled[0].Adapter != "qq-bot-1" {
		t.Fatalf("expected adapter qq-bot-1, got %s", disabled[0].Adapter)
	}

	snapshot := mgr.Snapshot()
	if len(snapshot.DisabledBindings) != 1 {
		t.Fatalf("expected 1 disabled binding in snapshot, got %d", len(snapshot.DisabledBindings))
	}

	if err := mgr.DisableBinding("qq-bot-1"); err != ErrNoChannelBound {
		t.Fatalf("expected ErrNoChannelBound for already-disabled, got %v", err)
	}

	if err := mgr.EnableBinding("qq-bot-1"); err != nil {
		t.Fatalf("EnableBinding: %v", err)
	}

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

	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should not be disabled after enable")
	}

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

	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	_ = mgr.Emit(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"})

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

	mgr.UnbindSession()

	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("disabled should be cleared after UnbindSession")
	}
	if len(mgr.DisabledBindings()) != 0 {
		t.Fatal("disabled bindings should be empty after UnbindSession")
	}
}

func TestApplyAdapterConfig_DisablesConfiguredAdapters(t *testing.T) {
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
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "channel-2",
	})

	if len(mgr.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active bindings, got %d", len(mgr.CurrentBindings()))
	}

	mgr.ApplyAdapterConfig(map[string]bool{
		"qq-bot-1": false,
		"tg-bot-1": true,
	})

	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("qq-bot-1 should be disabled after ApplyAdapterConfig")
	}

	if mgr.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg-bot-1 should NOT be disabled")
	}

	if len(mgr.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active binding, got %d", len(mgr.CurrentBindings()))
	}

	if len(mgr.DisabledBindings()) != 1 {
		t.Fatalf("expected 1 disabled binding, got %d", len(mgr.DisabledBindings()))
	}
}

func TestApplyAdapterConfig_AllDisabled(t *testing.T) {
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
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "channel-2",
	})

	mgr.ApplyAdapterConfig(map[string]bool{
		"qq-bot-1": false,
		"tg-bot-1": false,
	})

	if len(mgr.CurrentBindings()) != 0 {
		t.Fatalf("expected 0 active bindings, got %d", len(mgr.CurrentBindings()))
	}
	if len(mgr.DisabledBindings()) != 2 {
		t.Fatalf("expected 2 disabled bindings, got %d", len(mgr.DisabledBindings()))
	}
}

func TestApplyAdapterConfig_NoBindings(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	mgr.ApplyAdapterConfig(map[string]bool{
		"qq-bot-1": false,
	})

	if len(mgr.CurrentBindings()) != 0 {
		t.Fatalf("expected 0 active bindings, got %d", len(mgr.CurrentBindings()))
	}
}

func TestApplyAdapterConfig_Idempotent(t *testing.T) {
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

	mgr.ApplyAdapterConfig(map[string]bool{"qq-bot-1": false})
	mgr.ApplyAdapterConfig(map[string]bool{"qq-bot-1": false})

	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should still be disabled after double apply")
	}
	if len(mgr.DisabledBindings()) != 1 {
		t.Fatalf("expected 1 disabled binding, got %d", len(mgr.DisabledBindings()))
	}
}

// TestApplyAdapterConfig_EnablePreviouslyDisabled verifies that ApplyAdapterConfig
// does NOT re-enable adapters that are already disabled — enabling requires EnableBinding.
func TestApplyAdapterConfig_DoesNotReEnable(t *testing.T) {
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

	mgr.ApplyAdapterConfig(map[string]bool{"qq-bot-1": false})
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should be disabled")
	}

	mgr.ApplyAdapterConfig(map[string]bool{"qq-bot-1": true})
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("ApplyAdapterConfig should not re-enable disabled adapters")
	}
}

// TestSimulateRestartFlow simulates the full restart scenario:
// 1. User disables an adapter
// 2. App restarts (new Manager, fresh bindings from store)
// 3. ApplyAdapterConfig reads the persisted config and re-disables the adapter
func TestSimulateRestartFlow(t *testing.T) {

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
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "channel-2",
	})

	if len(mgr.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active, got %d", len(mgr.CurrentBindings()))
	}

	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("qq should be disabled")
	}
	if mgr.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg should be active")
	}

	mgr2 := NewManager()
	mgr2.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr2.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-1",
	})
	_, _ = mgr2.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "channel-2",
	})

	if len(mgr2.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active before apply, got %d", len(mgr2.CurrentBindings()))
	}

	mgr2.ApplyAdapterConfig(map[string]bool{
		"qq-bot-1": false,
		"tg-bot-1": true,
	})

	if !mgr2.IsBindingDisabled("qq-bot-1") {
		t.Fatal("qq should be disabled after restart + ApplyAdapterConfig")
	}
	if mgr2.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg should remain active")
	}
	if len(mgr2.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active after apply, got %d", len(mgr2.CurrentBindings()))
	}
}
