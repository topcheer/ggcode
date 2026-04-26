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

	// Verify binding stays in currentBindings but is marked muted
	bindings := mgr.CurrentBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding after mute (stays in currentBindings), got %d", len(bindings))
	}
	if !bindings[0].Muted {
		t.Fatal("expected binding to be marked Muted")
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

	// Verify all bindings are still present but muted
	allBindings := mgr.CurrentBindings()
	if len(allBindings) != 3 {
		t.Fatalf("expected 3 bindings after MuteAll (stays in currentBindings), got %d", len(allBindings))
	}
	for _, b := range allBindings {
		if !b.Muted {
			t.Fatalf("expected binding %s to be muted", b.Adapter)
		}
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

	// Verify qq-bot-1 still in currentBindings (muted), tg-bot-1 moved out (disabled)
	allBindings := mgr.CurrentBindings()
	if len(allBindings) != 1 {
		t.Fatalf("expected 1 binding after mute+disable, got %d", len(allBindings))
	}
	if allBindings[0].Adapter != "qq-bot-1" || !allBindings[0].Muted {
		t.Fatal("expected qq-bot-1 to be muted in currentBindings")
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

func TestMuteBindingCallsClose(t *testing.T) {
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

	// Register a sink that tracks Close calls
	sink := &dummySink{name: "qq-bot-1"}
	mgr.RegisterSink(sink)

	// Mute should call Close
	_ = mgr.MuteBinding("qq-bot-1")
	if !sink.closed {
		t.Fatal("expected Close() to be called on mute")
	}
}

func TestDisableBindingCallsClose(t *testing.T) {
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

	sink := &dummySink{name: "qq-bot-1"}
	mgr.RegisterSink(sink)

	_ = mgr.DisableBinding("qq-bot-1")
	if !sink.closed {
		t.Fatal("expected Close() to be called on disable")
	}
}

func TestMuteAllCallsClose(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformQQ, Adapter: "qq", ChannelID: "1"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformTelegram, Adapter: "tg", ChannelID: "2"})

	qqSink := &dummySink{name: "qq"}
	tgSink := &dummySink{name: "tg"}
	mgr.RegisterSink(qqSink)
	mgr.RegisterSink(tgSink)

	count, _ := mgr.MuteAll()
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}
	if !qqSink.closed {
		t.Fatal("expected qq Close() to be called")
	}
	if !tgSink.closed {
		t.Fatal("expected tg Close() to be called")
	}
}

func TestMuteBindingSetsDisconnected(t *testing.T) {
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

	// Simulate adapter publishing healthy state
	mgr.PublishAdapterState(AdapterState{
		Name:    "qq-bot-1",
		Healthy: true,
		Status:  "connected",
	})

	// Verify it's healthy
	snapshot := mgr.Snapshot()
	var state AdapterState
	for _, a := range snapshot.Adapters {
		if a.Name == "qq-bot-1" {
			state = a
		}
	}
	if !state.Healthy {
		t.Fatal("should be healthy before mute")
	}

	// Mute it
	_ = mgr.MuteBinding("qq-bot-1")

	// Verify adapter state is now disconnected
	snapshot = mgr.Snapshot()
	for _, a := range snapshot.Adapters {
		if a.Name == "qq-bot-1" {
			if a.Healthy {
				t.Fatal("should NOT be healthy after mute")
			}
			if a.Status != "disconnected" {
				t.Fatalf("expected status 'disconnected', got %q", a.Status)
			}
		}
	}
}

func TestDisableBindingSetsDisconnected(t *testing.T) {
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

	mgr.PublishAdapterState(AdapterState{
		Name:    "qq-bot-1",
		Healthy: true,
		Status:  "connected",
	})

	_ = mgr.DisableBinding("qq-bot-1")

	snapshot := mgr.Snapshot()
	for _, a := range snapshot.Adapters {
		if a.Name == "qq-bot-1" {
			if a.Healthy {
				t.Fatal("should NOT be healthy after disable")
			}
			if a.Status != "disconnected" {
				t.Fatalf("expected status 'disconnected', got %q", a.Status)
			}
		}
	}
}

func TestMuteAllSetsDisconnected(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformQQ, Adapter: "qq", ChannelID: "1"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/workspace/test", Platform: PlatformTelegram, Adapter: "tg", ChannelID: "2"})

	mgr.PublishAdapterState(AdapterState{Name: "qq", Healthy: true, Status: "connected"})
	mgr.PublishAdapterState(AdapterState{Name: "tg", Healthy: true, Status: "connected"})

	count, _ := mgr.MuteAll()
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}

	snapshot := mgr.Snapshot()
	for _, a := range snapshot.Adapters {
		if a.Name == "qq" || a.Name == "tg" {
			if a.Healthy {
				t.Fatalf("%s should NOT be healthy after MuteAll", a.Name)
			}
			if a.Status != "disconnected" {
				t.Fatalf("%s expected 'disconnected', got %q", a.Name, a.Status)
			}
		}
	}
}

// dummySink is a minimal Sink for testing
type dummySink struct {
	name   string
	closed bool
}

func (d *dummySink) Name() string { return d.name }
func (d *dummySink) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	return nil
}
func (d *dummySink) Close() error {
	d.closed = true
	return nil
}

// --- onRestart tests ---

func TestUnmuteBindingRestartsAdapter(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp", Platform: PlatformQQ, Adapter: "test-qq", ChannelID: "ch-1",
	})

	var restarted []string
	mgr.SetOnRestart(func(name string) error {
		restarted = append(restarted, name)
		return nil
	})

	if err := mgr.MuteBinding("test-qq"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}
	if err := mgr.UnmuteBinding("test-qq"); err != nil {
		t.Fatalf("UnmuteBinding: %v", err)
	}

	if len(restarted) != 1 || restarted[0] != "test-qq" {
		t.Fatalf("expected onRestart called with test-qq, got %v", restarted)
	}
}

func TestEnableBindingRestartsAdapter(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})

	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp", Platform: PlatformQQ, Adapter: "test-qq", ChannelID: "ch-1",
	})

	var restarted []string
	mgr.SetOnRestart(func(name string) error {
		restarted = append(restarted, name)
		return nil
	})

	if err := mgr.DisableBinding("test-qq"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}
	if err := mgr.EnableBinding("test-qq"); err != nil {
		t.Fatalf("EnableBinding: %v", err)
	}

	if len(restarted) != 1 || restarted[0] != "test-qq" {
		t.Fatalf("expected onRestart called with test-qq, got %v", restarted)
	}
}

func TestUnmuteAllRestartsAllAdapters(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})

	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformQQ, Adapter: "qq-1", ChannelID: "ch-1"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformQQ, Adapter: "qq-2", ChannelID: "ch-2"})

	var restarted []string
	mgr.SetOnRestart(func(name string) error {
		restarted = append(restarted, name)
		return nil
	})

	count, _ := mgr.MuteAll()
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}

	count, _ = mgr.UnmuteAll()
	if count != 2 {
		t.Fatalf("expected 2 unmuted, got %d", count)
	}
	if len(restarted) != 2 {
		t.Fatalf("expected 2 restarts, got %d", len(restarted))
	}
}

func TestUnmuteNoRestartWhenNoCallback(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})

	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformQQ, Adapter: "test-qq", ChannelID: "ch-1"})

	if err := mgr.MuteBinding("test-qq"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}
	// No onRestart set — should not panic
	if err := mgr.UnmuteBinding("test-qq"); err != nil {
		t.Fatalf("UnmuteBinding: %v", err)
	}
}

// --- persistBinding / reload tests ---

func TestPersistBindingStripsMuted(t *testing.T) {
	var saved ChannelBinding
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	mgr.SetBindingStore(&stubBindingStoreForMute{
		save: func(b ChannelBinding) error {
			saved = b
			return nil
		},
	})

	b, err := mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformQQ, Adapter: "qq-1", ChannelID: "ch-1"})
	if err != nil {
		t.Fatal(err)
	}

	// Mute it
	if err := mgr.MuteBinding("qq-1"); err != nil {
		t.Fatal(err)
	}

	// Manually trigger persistBinding — should strip Muted
	b.Muted = true
	if err := mgr.persistBinding(b); err != nil {
		t.Fatal(err)
	}
	if saved.Muted {
		t.Fatal("persistBinding should strip Muted flag before saving")
	}
}

func TestReloadBindingClearsMuted(t *testing.T) {
	// Simulate a store that returns a binding with Muted=true (e.g. corrupted data)
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	mgr.SetBindingStore(&stubBindingStoreForMute{
		listByWorkspace: func(ws string) ([]ChannelBinding, error) {
			return []ChannelBinding{{
				Workspace: "/tmp",
				Platform:  PlatformQQ,
				Adapter:   "qq-1",
				ChannelID: "ch-1",
				Muted:     true, // Should be cleared on reload
			}}, nil
		},
	})

	// Reload bindings
	if err := mgr.SetBindingStore(mgr.bindingStore); err != nil {
		t.Fatal(err)
	}

	bindings := mgr.CurrentBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].Muted {
		t.Fatal("reloadBindingLocked should clear Muted flag")
	}
}

func TestHandleInboundDropsMuted(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformQQ, Adapter: "qq-1", ChannelID: "ch-1"})

	bridge := &trackingBridge{}
	mgr.SetBridge(bridge)

	if err := mgr.MuteBinding("qq-1"); err != nil {
		t.Fatal(err)
	}

	err := mgr.HandleInbound(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq-1", ChannelID: "ch-1", MessageID: "msg-1"},
		Text:     "hello",
	})
	if err != nil {
		t.Fatalf("HandleInbound: %v", err)
	}
	if bridge.calls != 0 {
		t.Fatal("HandleInbound should drop messages for muted adapters")
	}
}

// trackingBridge records how many times SubmitInboundMessage is called.
type trackingBridge struct{ calls int }

func (b *trackingBridge) SubmitInboundMessage(_ context.Context, _ InboundMessage) error {
	b.calls++
	return nil
}

func TestHandlePairingInboundDropsMuted(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformQQ, Adapter: "qq-1", ChannelID: "ch-1"})

	if err := mgr.MuteBinding("qq-1"); err != nil {
		t.Fatal(err)
	}

	result, err := mgr.HandlePairingInbound(InboundMessage{
		Envelope: Envelope{Adapter: "qq-1", ChannelID: "ch-1"},
		Text:     "pair",
	})
	if err != nil {
		t.Fatalf("HandlePairingInbound: %v", err)
	}
	// Should return empty result (silently ignored).
	if result.Bound {
		t.Fatal("HandlePairingInbound should silently ignore muted adapters")
	}
}

// --- stub stores ---

type stubBindingStoreForMute struct {
	save            func(ChannelBinding) error
	listByWorkspace func(string) ([]ChannelBinding, error)
}

func (s *stubBindingStoreForMute) Save(b ChannelBinding) error {
	if s.save != nil {
		return s.save(b)
	}
	return nil
}
func (s *stubBindingStoreForMute) Delete(string, string) error { return nil }
func (s *stubBindingStoreForMute) List() ([]ChannelBinding, error) {
	return nil, nil
}
func (s *stubBindingStoreForMute) ListByWorkspace(ws string) ([]ChannelBinding, error) {
	if s.listByWorkspace != nil {
		return s.listByWorkspace(ws)
	}
	return nil, nil
}
func (s *stubBindingStoreForMute) ListByAdapter(string) ([]ChannelBinding, error) {
	return nil, nil
}

func TestPublishAdapterStateIgnoredWhenMuted(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	_, _ = mgr.BindChannel(ChannelBinding{Workspace: "/tmp", Platform: PlatformTelegram, Adapter: "tg-1", ChannelID: "ch-1"})

	// Simulate adapter publishing its initial state
	mgr.PublishAdapterState(AdapterState{
		Name:     "tg-1",
		Platform: PlatformTelegram,
		Healthy:  true,
		Status:   "connected",
	})

	if err := mgr.MuteBinding("tg-1"); err != nil {
		t.Fatal(err)
	}

	// State should be "disconnected" (set by stopAdapter)
	state := mgr.adapters["tg-1"]
	if state.Status != "disconnected" {
		t.Fatalf("expected status 'disconnected' after mute, got '%s'", state.Status)
	}

	// Simulate the old goroutine publishing a late error (like 409)
	mgr.PublishAdapterState(AdapterState{
		Name:      "tg-1",
		Platform:  PlatformTelegram,
		Healthy:   false,
		Status:    "error",
		LastError: "poll updates: Telegram API [409]",
	})

	// State should remain "disconnected", not overwritten to "error"
	state = mgr.adapters["tg-1"]
	if state.Status != "disconnected" {
		t.Fatalf("expected status 'disconnected' after late publishState, got '%s'", state.Status)
	}
	if state.LastError != "" {
		t.Fatalf("expected no last error, got '%s'", state.LastError)
	}
}

func TestMuteAllExcept(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	// Bind 3 channels
	for _, name := range []string{"qq", "telegram", "discord"} {
		_, err := mgr.BindChannel(ChannelBinding{
			Workspace: "/workspace/test",
			Platform:  PlatformQQ,
			Adapter:   name,
			ChannelID: "ch-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// MuteAllExcept("telegram") — should mute qq and discord, keep telegram
	count, err := mgr.MuteAllExcept("telegram")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}

	// Verify telegram is NOT muted
	snap := mgr.Snapshot()
	for _, b := range snap.CurrentBindings {
		switch b.Adapter {
		case "telegram":
			if b.Muted {
				t.Error("telegram should NOT be muted")
			}
		case "qq", "discord":
			if !b.Muted {
				t.Errorf("%s should be muted", b.Adapter)
			}
		}
	}
}

func TestMuteAllExceptEmptyExclude(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	for _, name := range []string{"qq", "telegram"} {
		mgr.BindChannel(ChannelBinding{
			Workspace: "/workspace/test",
			Platform:  PlatformQQ,
			Adapter:   name,
			ChannelID: "ch-" + name,
		})
	}

	// Empty exclude → mute all
	count, err := mgr.MuteAllExcept("")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}

	snap := mgr.Snapshot()
	for _, b := range snap.CurrentBindings {
		if !b.Muted {
			t.Errorf("%s should be muted when exclude is empty", b.Adapter)
		}
	}
}

func TestMuteAllExceptNonexistent(t *testing.T) {
	mgr := NewManager()
	mgr.BindSession(SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})

	mgr.BindChannel(ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-qq",
	})

	// Exclude a name that doesn't exist — qq should still be muted
	count, err := mgr.MuteAllExcept("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 muted, got %d", count)
	}
}
