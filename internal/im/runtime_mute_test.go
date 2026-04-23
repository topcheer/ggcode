package im

import (
	"context"
	"testing"
)

func TestMuteUnmuteBinding(t *testing.T) {
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

	// Verify not muted
	if mgr.IsBindingMuted("qq-bot-1") {
		t.Fatal("should not be muted initially")
	}

	// Mute the binding
	if err := mgr.MuteBinding("qq-bot-1"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}

	// Verify it's removed from currentBindings
	bindings := mgr.CurrentBindings()
	if len(bindings) != 0 {
		t.Fatalf("expected 0 active bindings after mute, got %d", len(bindings))
	}

	// Verify it's muted
	if !mgr.IsBindingMuted("qq-bot-1") {
		t.Fatal("should be muted")
	}

	// Verify muted bindings snapshot
	muted := mgr.MutedBindings()
	if len(muted) != 1 {
		t.Fatalf("expected 1 muted binding, got %d", len(muted))
	}
	if muted[0].Adapter != "qq-bot-1" {
		t.Fatalf("expected adapter qq-bot-1, got %s", muted[0].Adapter)
	}
	if muted[0].ChannelID != "channel-123" {
		t.Fatalf("expected channelID channel-123, got %s", muted[0].ChannelID)
	}

	// Verify snapshot includes muted bindings
	snapshot := mgr.Snapshot()
	if len(snapshot.MutedBindings) != 1 {
		t.Fatalf("expected 1 muted binding in snapshot, got %d", len(snapshot.MutedBindings))
	}

	// Mute again should fail
	if err := mgr.MuteBinding("qq-bot-1"); err != ErrNoChannelBound {
		t.Fatalf("expected ErrNoChannelBound for already-muted, got %v", err)
	}

	// Unmute the binding
	if err := mgr.UnmuteBinding("qq-bot-1"); err != nil {
		t.Fatalf("UnmuteBinding: %v", err)
	}

	// Verify it's back in currentBindings
	bindings = mgr.CurrentBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding after unmute, got %d", len(bindings))
	}
	if bindings[0].Adapter != "qq-bot-1" {
		t.Fatalf("expected adapter qq-bot-1, got %s", bindings[0].Adapter)
	}
	if bindings[0].ChannelID != "channel-123" {
		t.Fatalf("expected channelID channel-123, got %s", bindings[0].ChannelID)
	}

	// Verify it's not muted
	if mgr.IsBindingMuted("qq-bot-1") {
		t.Fatal("should not be muted after unmute")
	}

	// Unmute again should fail
	if err := mgr.UnmuteBinding("qq-bot-1"); err != ErrNoChannelBound {
		t.Fatalf("expected ErrNoChannelBound for already-active, got %v", err)
	}
}

func TestMuteAllUnmuteAll(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind three channels
	adapters := []struct {
		adapter   string
		platform  Platform
		channelID string
	}{
		{"qq-bot-1", PlatformQQ, "ch-1"},
		{"tg-bot-1", PlatformTelegram, "ch-2"},
		{"discord-bot-1", PlatformDiscord, "ch-3"},
	}
	for _, a := range adapters {
		_, err := mgr.BindChannel(ChannelBinding{
			Workspace: "/workspace/test",
			Platform:  a.platform,
			Adapter:   a.adapter,
			ChannelID: a.channelID,
		})
		if err != nil {
			t.Fatalf("BindChannel %s: %v", a.adapter, err)
		}
	}

	// Mute all
	count, err := mgr.MuteAll()
	if err != nil {
		t.Fatalf("MuteAll: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 muted, got %d", count)
	}

	// Verify all are muted
	if len(mgr.CurrentBindings()) != 0 {
		t.Fatal("expected 0 active bindings after MuteAll")
	}
	if len(mgr.MutedBindings()) != 3 {
		t.Fatalf("expected 3 muted bindings, got %d", len(mgr.MutedBindings()))
	}

	// MuteAll again should return 0 (nothing to mute)
	count, err = mgr.MuteAll()
	if err != nil {
		t.Fatalf("MuteAll (empty): %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 on second MuteAll, got %d", count)
	}

	// Unmute all
	count, err = mgr.UnmuteAll()
	if err != nil {
		t.Fatalf("UnmuteAll: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 unmuted, got %d", count)
	}

	// Verify all are active again
	if len(mgr.CurrentBindings()) != 3 {
		t.Fatalf("expected 3 active bindings after UnmuteAll, got %d", len(mgr.CurrentBindings()))
	}
	if len(mgr.MutedBindings()) != 0 {
		t.Fatalf("expected 0 muted bindings after UnmuteAll, got %d", len(mgr.MutedBindings()))
	}

	// Verify snapshot is clean
	snapshot := mgr.Snapshot()
	if len(snapshot.MutedBindings) != 0 {
		t.Fatalf("expected 0 muted in snapshot after UnmuteAll, got %d", len(snapshot.MutedBindings))
	}
	if len(snapshot.CurrentBindings) != 3 {
		t.Fatalf("expected 3 active in snapshot, got %d", len(snapshot.CurrentBindings))
	}
}

func TestMuteAndDisableAreIndependent(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind two channels
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "ch-2",
	})

	// Mute qq-bot-1
	if err := mgr.MuteBinding("qq-bot-1"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}

	// Disable tg-bot-1
	if err := mgr.DisableBinding("tg-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	// Verify no active bindings
	if len(mgr.CurrentBindings()) != 0 {
		t.Fatal("expected 0 active bindings")
	}

	// Verify muted and disabled are tracked separately
	if !mgr.IsBindingMuted("qq-bot-1") {
		t.Fatal("qq-bot-1 should be muted")
	}
	if mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("qq-bot-1 should NOT be disabled")
	}
	if !mgr.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg-bot-1 should be disabled")
	}
	if mgr.IsBindingMuted("tg-bot-1") {
		t.Fatal("tg-bot-1 should NOT be muted")
	}

	// Snapshot should have both
	snapshot := mgr.Snapshot()
	if len(snapshot.MutedBindings) != 1 {
		t.Fatalf("expected 1 muted in snapshot, got %d", len(snapshot.MutedBindings))
	}
	if len(snapshot.DisabledBindings) != 1 {
		t.Fatalf("expected 1 disabled in snapshot, got %d", len(snapshot.DisabledBindings))
	}

	// UnmuteAll should only restore muted, not disabled
	count, _ := mgr.UnmuteAll()
	if count != 1 {
		t.Fatalf("expected 1 unmuted, got %d", count)
	}
	if len(mgr.CurrentBindings()) != 1 {
		t.Fatal("expected 1 active binding after UnmuteAll")
	}
	if mgr.CurrentBindings()[0].Adapter != "qq-bot-1" {
		t.Fatal("expected qq-bot-1 to be restored")
	}
	if !mgr.IsBindingDisabled("tg-bot-1") {
		t.Fatal("tg-bot-1 should still be disabled after UnmuteAll")
	}

	// EnableAll should only restore disabled, not muted
	// First re-mute qq
	_ = mgr.MuteBinding("qq-bot-1")
	// EnableBinding is single, so just check state
	if err := mgr.EnableBinding("tg-bot-1"); err != nil {
		t.Fatalf("EnableBinding: %v", err)
	}
	if !mgr.IsBindingMuted("qq-bot-1") {
		t.Fatal("qq-bot-1 should still be muted after enabling tg")
	}
}

func TestMuteBindingSkipsInbound(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind and mute
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})
	_ = mgr.MuteBinding("qq-bot-1")

	// Inbound should be silently dropped (no bridge set, but the key test
	// is that it returns nil without error rather than trying to process)
	err := mgr.HandleInbound(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq-bot-1"},
		Text:     "should be dropped",
	})
	if err != nil {
		t.Fatalf("HandleInbound for muted adapter should return nil, got %v", err)
	}
}

func TestUnbindSessionClearsMuted(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})

	_ = mgr.MuteBinding("qq-bot-1")

	// Unbind session should clear muted
	mgr.UnbindSession()

	if mgr.IsBindingMuted("qq-bot-1") {
		t.Fatal("muted should be cleared after UnbindSession")
	}
	if len(mgr.MutedBindings()) != 0 {
		t.Fatal("muted bindings should be empty after UnbindSession")
	}
}

func TestMuteAllPreservesDisabled(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind three channels
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformQQ, Adapter: "qq", ChannelID: "1"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformTelegram, Adapter: "tg", ChannelID: "2"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformDiscord, Adapter: "dc", ChannelID: "3"})

	// Disable qq
	_ = mgr.DisableBinding("qq")

	// Now only tg and dc are active. MuteAll should mute those 2.
	count, _ := mgr.MuteAll()
	if count != 2 {
		t.Fatalf("expected 2 muted (only active ones), got %d", count)
	}

	// qq should still be disabled, not muted
	if !mgr.IsBindingDisabled("qq") {
		t.Fatal("qq should still be disabled")
	}
	if mgr.IsBindingMuted("qq") {
		t.Fatal("qq should NOT be muted (it was disabled, not active)")
	}

	// UnmuteAll should restore tg and dc, but qq stays disabled
	count, _ = mgr.UnmuteAll()
	if count != 2 {
		t.Fatalf("expected 2 unmuted, got %d", count)
	}
	if !mgr.IsBindingDisabled("qq") {
		t.Fatal("qq should still be disabled after UnmuteAll")
	}
	if len(mgr.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 active after UnmuteAll, got %d", len(mgr.CurrentBindings()))
	}
}

func TestMuteBindingCancelsAdapter(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})

	// Register a cancel func (simulates what startConfiguredAdapter does)
	cancelled := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.RegisterAdapterCancel("qq-bot-1", func() { cancelled = true; cancel() })

	// Register a sink (simulates the adapter)
	mgr.RegisterSink(&dummySink{name: "qq-bot-1"})

	// Mute should cancel the adapter and unregister the sink
	if err := mgr.MuteBinding("qq-bot-1"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}
	if !cancelled {
		t.Fatal("expected adapter cancel to be called on mute")
	}
	if ctx.Err() == nil {
		t.Fatal("expected adapter context to be cancelled")
	}
	// Sink should be removed
	snapshot := mgr.Snapshot()
	for _, a := range snapshot.Adapters {
		if a.Name == "qq-bot-1" {
			t.Fatal("qq-bot-1 adapter should not appear in snapshot after mute")
		}
	}
}

func TestDisableBindingCancelsAdapter(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})

	cancelled := false
	mgr.RegisterAdapterCancel("qq-bot-1", func() { cancelled = true })

	if err := mgr.DisableBinding("qq-bot-1"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}
	if !cancelled {
		t.Fatal("expected adapter cancel to be called on disable")
	}
}

func TestMuteAllCancelsAllAdapters(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformQQ, Adapter: "qq", ChannelID: "1"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformTelegram, Adapter: "tg", ChannelID: "2"})

	qqCancelled := false
	tgCancelled := false
	mgr.RegisterAdapterCancel("qq", func() { qqCancelled = true })
	mgr.RegisterAdapterCancel("tg", func() { tgCancelled = true })

	count, _ := mgr.MuteAll()
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}
	if !qqCancelled {
		t.Fatal("qq adapter should be cancelled")
	}
	if !tgCancelled {
		t.Fatal("tg adapter should be cancelled")
	}
}

func TestUnbindSessionClearsAdapterCancels(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "ch-1",
	})

	cancelled := false
	mgr.RegisterAdapterCancel("qq-bot-1", func() { cancelled = true })
	_ = mgr.MuteBinding("qq-bot-1")

	// cancelled was called during mute above
	if !cancelled {
		t.Fatal("expected cancel to be called during mute")
	}

	// UnbindSession should reset adapterCancels
	mgr.UnbindSession()

	// After unbind, a fresh cancel registration should work
	cancelled2 := false
	mgr.RegisterAdapterCancel("qq-bot-1", func() { cancelled2 = true })
	// No panic means adapterCancels was properly reinitialized
	_ = cancelled2
}

func TestStopAdapterIdempotent(t *testing.T) {
	mgr := NewManager()
	// stopAdapter on non-existent adapter should not panic
	mgr.mu.Lock()
	mgr.stopAdapter("nonexistent")
	mgr.mu.Unlock()

	// stopAdapter with nil cancel should not panic
	mgr.RegisterAdapterCancel("test", nil)
	mgr.mu.Lock()
	mgr.stopAdapter("test")
	mgr.mu.Unlock()
}

// dummySink is a minimal Sink for testing
type dummySink struct {
	name string
}

func (d *dummySink) Name() string { return d.name }
func (d *dummySink) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	return nil
}
