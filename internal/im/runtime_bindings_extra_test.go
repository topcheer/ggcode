package im

import (
	"context"
	"testing"
)

// extraSink is a test Sink distinct from stubSink in runtime_test.go.
type extraSink struct {
	nameVal string
	sent    []OutboundEvent
	err     error
}

func (s *extraSink) Name() string { return s.nameVal }

func (s *extraSink) Send(_ context.Context, _ ChannelBinding, event OutboundEvent) error {
	s.sent = append(s.sent, event)
	return s.err
}

func (s *extraSink) count() int { return len(s.sent) }
func (s *extraSink) reset()     { s.sent = nil }
func (s *extraSink) lastText() string {
	if len(s.sent) == 0 {
		return ""
	}
	return s.sent[len(s.sent)-1].Text
}

var _ Sink = (*extraSink)(nil)

func TestDeleteBinding(t *testing.T) {
	store := NewMemoryBindingStore()
	mgr := NewManager()
	mgr.SetBindingStore(store)

	// Save a binding first
	binding := ChannelBinding{
		Workspace: "/test/ws",
		Adapter:   "qq",
		ChannelID: "channel-1",
	}
	mgr.RegisterSink(&extraSink{nameVal: "qq"})
	mgr.currentBindings["qq"] = &binding
	_ = store.Save(binding)

	if err := mgr.DeleteBinding("qq", "/test/ws"); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	if _, ok := mgr.currentBindings["qq"]; ok {
		t.Error("binding should be removed from currentBindings")
	}
	list, _ := store.List()
	if len(list) != 0 {
		t.Errorf("store should be empty, got %d bindings", len(list))
	}

	// No store
	mgr2 := NewManager()
	if err := mgr2.DeleteBinding("qq", "/test/ws"); err == nil {
		t.Error("expected error with no store")
	}
}

func TestUnbindAdapter(t *testing.T) {
	store := NewMemoryBindingStore()
	mgr := NewManager()
	mgr.SetBindingStore(store)

	binding := ChannelBinding{
		Workspace: "/test/ws",
		Adapter:   "tg",
		ChannelID: "ch-1",
	}
	mgr.RegisterSink(&extraSink{nameVal: "tg"})
	mgr.currentBindings["tg"] = &binding
	_ = store.Save(binding)

	if err := mgr.UnbindAdapter("tg"); err != nil {
		t.Fatalf("UnbindAdapter: %v", err)
	}
	if _, ok := mgr.currentBindings["tg"]; ok {
		t.Error("binding should be removed")
	}
	// store should also be cleared
	list, _ := store.ListByAdapter("tg")
	if len(list) != 0 {
		t.Errorf("store should be empty, got %d", len(list))
	}

	// No binding at all
	if err := mgr.UnbindAdapter("nonexistent"); err == nil {
		t.Error("expected ErrNoChannelBound for nonexistent adapter")
	}
}

func TestClearChannelByAdapter(t *testing.T) {
	store := NewMemoryBindingStore()
	mgr := NewManager()
	mgr.SetBindingStore(store)

	binding := ChannelBinding{
		Workspace:            "/test/ws",
		Adapter:              "discord",
		ChannelID:            "ch-1",
		ThreadID:             "th-1",
		LastInboundMessageID: "msg-1",
	}
	_ = store.Save(binding)
	mgr.currentBindings["discord"] = &binding

	if err := mgr.ClearChannelByAdapter("discord"); err != nil {
		t.Fatalf("ClearChannelByAdapter: %v", err)
	}

	b := mgr.currentBindings["discord"]
	if b.ChannelID != "" {
		t.Error("ChannelID should be cleared")
	}
	if b.ThreadID != "" {
		t.Error("ThreadID should be cleared")
	}
	if b.LastInboundMessageID != "" {
		t.Error("LastInboundMessageID should be cleared")
	}

	// nonexistent adapter
	if err := mgr.ClearChannelByAdapter("nonexistent"); err == nil {
		t.Error("expected error for nonexistent adapter")
	}
}

func TestDisableAll(t *testing.T) {
	mgr := NewManager()
	mgr.RegisterSink(&extraSink{nameVal: "qq"})
	mgr.RegisterSink(&extraSink{nameVal: "tg"})
	mgr.currentBindings["qq"] = &ChannelBinding{Workspace: "/ws", Adapter: "qq", ChannelID: "ch1"}
	mgr.currentBindings["tg"] = &ChannelBinding{Workspace: "/ws", Adapter: "tg", ChannelID: "ch2"}

	count, err := mgr.DisableAll()
	if err != nil {
		t.Fatalf("DisableAll: %v", err)
	}
	if count != 2 {
		t.Errorf("DisableAll count = %d, want 2", count)
	}
	if len(mgr.currentBindings) != 0 {
		t.Error("all bindings should be disabled (removed from current)")
	}
	if len(mgr.disabledBindings) != 2 {
		t.Errorf("disabledBindings = %d, want 2", len(mgr.disabledBindings))
	}
}

func TestEnableAll(t *testing.T) {
	mgr := NewManager()
	mgr.RegisterSink(&extraSink{nameVal: "qq"})
	mgr.disabledBindings["qq"] = &ChannelBinding{Workspace: "/ws", Adapter: "qq", ChannelID: "ch1"}
	mgr.disabledBindings["tg"] = &ChannelBinding{Workspace: "/ws", Adapter: "tg", ChannelID: "ch2"}

	count, err := mgr.EnableAll()
	if err != nil {
		t.Fatalf("EnableAll: %v", err)
	}
	if count != 2 {
		t.Errorf("EnableAll count = %d, want 2", count)
	}
	if len(mgr.disabledBindings) != 0 {
		t.Error("disabledBindings should be empty")
	}
	if len(mgr.currentBindings) != 2 {
		t.Errorf("currentBindings = %d, want 2", len(mgr.currentBindings))
	}
}

func TestEnableAllWithRestart(t *testing.T) {
	mgr := NewManager()
	mgr.RegisterSink(&extraSink{nameVal: "qq"})
	mgr.disabledBindings["qq"] = &ChannelBinding{Workspace: "/ws", Adapter: "qq", ChannelID: "ch1"}

	restarted := false
	mgr.SetOnRestart(func(name string) error {
		restarted = true
		return nil
	})

	count, _ := mgr.EnableAll()
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
	if !restarted {
		t.Error("onRestart should have been called")
	}
}

func TestHasActiveBindings(t *testing.T) {
	mgr := NewManager()
	if mgr.HasActiveBindings() {
		t.Error("new manager should have no active bindings")
	}
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}
	if !mgr.HasActiveBindings() {
		t.Error("should have active binding")
	}
}

func TestHasNonInteractiveBindings(t *testing.T) {
	mgr := NewManager()
	if mgr.HasNonInteractiveBindings(nil) {
		t.Error("empty manager should have no non-interactive bindings")
	}

	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "ch2"}

	// No adapters sent interactively
	if !mgr.HasNonInteractiveBindings(nil) {
		t.Error("should have non-interactive when nothing was sent")
	}

	// Both adapters sent
	sent := map[string]string{"qq": "msg1", "tg": "msg2"}
	if mgr.HasNonInteractiveBindings(sent) {
		t.Error("should not have non-interactive when all sent")
	}

	// Only one adapter sent
	sent2 := map[string]string{"qq": "msg1"}
	if !mgr.HasNonInteractiveBindings(sent2) {
		t.Error("should have non-interactive for tg")
	}
}

func TestBindingByAdapter(t *testing.T) {
	snap := StatusSnapshot{
		CurrentBindings: []ChannelBinding{
			{Adapter: "qq", ChannelID: "ch1"},
			{Adapter: "tg", ChannelID: "ch2"},
		},
	}
	if b := snap.BindingByAdapter("qq"); b == nil || b.ChannelID != "ch1" {
		t.Error("should find qq binding")
	}
	if b := snap.BindingByAdapter("nonexistent"); b != nil {
		t.Error("should return nil for nonexistent")
	}
}

func TestAllPersistedBindings(t *testing.T) {
	mgr := NewManager()
	if bindings := mgr.AllPersistedBindings(); bindings != nil {
		t.Error("nil store should return nil")
	}

	store := NewMemoryBindingStore()
	mgr.SetBindingStore(store)
	_ = store.Save(ChannelBinding{Workspace: "/ws", Adapter: "qq", ChannelID: "ch1"})

	bindings := mgr.AllPersistedBindings()
	if len(bindings) != 1 {
		t.Errorf("expected 1 binding, got %d", len(bindings))
	}
}

func TestResolveOutputMode(t *testing.T) {
	// nil manager
	if mode := (*Manager)(nil).ResolveOutputMode("qq", "verbose"); mode != "verbose" {
		t.Errorf("nil manager: %q", mode)
	}

	mgr := NewManager()
	// unknown adapter
	if mode := mgr.ResolveOutputMode("qq", "verbose"); mode != "verbose" {
		t.Errorf("unknown adapter: %q", mode)
	}

	// wechat adapter with verbose default
	mgr.adapters["wechat"] = AdapterState{Platform: PlatformWechat}
	if mode := mgr.ResolveOutputMode("wechat", "verbose"); mode != WechatDefaultOutputMode {
		t.Errorf("wechat verbose: %q, want %q", mode, WechatDefaultOutputMode)
	}
	if mode := mgr.ResolveOutputMode("wechat", ""); mode != WechatDefaultOutputMode {
		t.Errorf("wechat empty: %q, want %q", mode, WechatDefaultOutputMode)
	}
	// wechat with explicit quiet should keep quiet
	if mode := mgr.ResolveOutputMode("wechat", "quiet"); mode != "quiet" {
		t.Errorf("wechat quiet: %q", mode)
	}

	// non-wechat adapter should pass through
	mgr.adapters["qq"] = AdapterState{Platform: PlatformQQ}
	if mode := mgr.ResolveOutputMode("qq", "verbose"); mode != "verbose" {
		t.Errorf("qq verbose: %q", mode)
	}
}

func TestEmitExcept(t *testing.T) {
	mgr := NewManager()
	sink1 := &extraSink{nameVal: "qq"}
	sink2 := &extraSink{nameVal: "tg"}
	mgr.RegisterSink(sink1)
	mgr.RegisterSink(sink2)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "ch2"}

	// Empty excludeAdapter should fall through to Emit (send to all)
	err := mgr.EmitExcept(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "")
	if err != nil {
		t.Fatalf("EmitExcept empty exclude: %v", err)
	}
	if sink1.count() != 1 || sink2.count() != 1 {
		t.Errorf("both sinks should receive: qq=%d tg=%d", sink1.count(), sink2.count())
	}

	// Exclude qq
	sink1.reset()
	sink2.reset()
	err = mgr.EmitExcept(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}, "qq")
	if err != nil {
		t.Fatalf("EmitExcept qq: %v", err)
	}
	if sink1.count() != 0 {
		t.Errorf("qq should be excluded: %d", sink1.count())
	}
	if sink2.count() != 1 {
		t.Errorf("tg should receive: %d", sink2.count())
	}
}

func TestEmitExceptAdapters(t *testing.T) {
	mgr := NewManager()
	sink1 := &extraSink{nameVal: "qq"}
	sink2 := &extraSink{nameVal: "tg"}
	sink3 := &extraSink{nameVal: "discord"}
	mgr.RegisterSink(sink1)
	mgr.RegisterSink(sink2)
	mgr.RegisterSink(sink3)
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "ch1"}
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "ch2"}
	mgr.currentBindings["discord"] = &ChannelBinding{Adapter: "discord", ChannelID: "ch3"}

	exclude := map[string]bool{"qq": true, "discord": true}
	err := mgr.EmitExceptAdapters(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}, exclude)
	if err != nil {
		t.Fatalf("EmitExceptAdapters: %v", err)
	}
	if sink1.count() != 0 {
		t.Errorf("qq excluded: %d", sink1.count())
	}
	if sink2.count() != 1 {
		t.Errorf("tg should receive: %d", sink2.count())
	}
	if sink3.count() != 0 {
		t.Errorf("discord excluded: %d", sink3.count())
	}

	// Exclude all -> ErrNoChannelBound
	excludeAll := map[string]bool{"qq": true, "tg": true, "discord": true}
	err = mgr.EmitExceptAdapters(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}, excludeAll)
	if err != ErrNoChannelBound {
		t.Errorf("exclude all: %v", err)
	}
}
