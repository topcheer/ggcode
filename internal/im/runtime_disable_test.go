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

	// Unbind session should clear both active and disabled
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

	// Bind two adapters
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

	// Both should be active
	if len(mgr.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active bindings, got %d", len(mgr.CurrentBindings()))
	}

	// Apply config: qq disabled, tg enabled
	mgr.ApplyAdapterConfig(map[string]bool{
		"qq-bot-1": false,
		"tg-bot-1": true,
	})

	// qq should be disabled
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("qq-bot-1 should be disabled after ApplyAdapterConfig")
	}
	// tg should still be active
	if mgr.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg-bot-1 should NOT be disabled")
	}
	// Active count should be 1
	if len(mgr.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active binding, got %d", len(mgr.CurrentBindings()))
	}
	// Disabled count should be 1
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

	// Disable all
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

	// No bindings — should not panic
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

	// Apply twice
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

	// First disable
	mgr.ApplyAdapterConfig(map[string]bool{"qq-bot-1": false})
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("should be disabled")
	}

	// Apply config with enabled=true should NOT move it back
	// (ApplyAdapterConfig only disables, doesn't enable)
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
	// Step 1: Initial setup with all adapters enabled
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

	// Verify all active
	if len(mgr.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active, got %d", len(mgr.CurrentBindings()))
	}

	// Step 2: User disables qq via UI (DisableBinding + config persist)
	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}
	// Simulate config.SetIMAdapterEnabled("qq-bot-1", false) — tested separately

	// Verify in-memory state
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("qq should be disabled")
	}
	if mgr.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg should be active")
	}

	// Step 3: Simulate restart — new Manager, bindings loaded from store
	mgr2 := NewManager()
	mgr2.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})
	// Bindings come from store (both, since store doesn't know about disabled)
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

	// Before ApplyAdapterConfig, both are active (simulating fresh load)
	if len(mgr2.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active before apply, got %d", len(mgr2.CurrentBindings()))
	}

	// Step 4: Apply config (which was persisted: qq=false, tg=true)
	mgr2.ApplyAdapterConfig(map[string]bool{
		"qq-bot-1": false,
		"tg-bot-1": true,
	})

	// Now qq should be disabled again, tg active
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
