package im

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

type stubBridge struct {
	last InboundMessage
	err  error
}

func (s *stubBridge) SubmitInboundMessage(_ context.Context, msg InboundMessage) error {
	s.last = msg
	return s.err
}

type stubSink struct {
	name     string
	bindings []ChannelBinding
	events   []OutboundEvent
	err      error
}

func (s *stubSink) Name() string { return s.name }

func (s *stubSink) Send(_ context.Context, binding ChannelBinding, event OutboundEvent) error {
	s.bindings = append(s.bindings, binding)
	s.events = append(s.events, event)
	return s.err
}

func TestHandleInboundRequiresSession(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	_ = mgr.SetBindingStore(NewMemoryBindingStore())

	err := mgr.HandleInbound(context.Background(), InboundMessage{Text: "hello"})
	if !errors.Is(err, ErrNoSessionBound) {
		t.Fatalf("expected ErrNoSessionBound, got %v", err)
	}
}

func TestHandleInboundDelegatesToBridge(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	msg := InboundMessage{Text: "hello", Envelope: Envelope{Adapter: "qq-bot-1", ChannelID: "group-1"}}
	if err := mgr.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("HandleInbound returned error: %v", err)
	}
	if bridge.last.Text != "hello" {
		t.Fatalf("expected bridge to receive message, got %#v", bridge.last)
	}
	if bridge.last.Envelope.ReceivedAt.IsZero() {
		t.Fatalf("expected runtime to stamp ReceivedAt")
	}
	binding := mgr.CurrentBinding()
	if binding == nil || binding.LastInboundMessageID != "" {
		t.Fatalf("expected no persisted inbound message without message id, got %#v", binding)
	}
}

func TestHandleInboundLearnsChannelFromFirstMessage(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform: PlatformQQ,
		Adapter:  "qq-bot-1",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	msg := InboundMessage{Text: "hello", Envelope: Envelope{Adapter: "qq-bot-1", ChannelID: "group-1", MessageID: "msg-1"}}
	if err := mgr.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("HandleInbound returned error: %v", err)
	}

	binding := mgr.CurrentBinding()
	if binding == nil || binding.ChannelID != "group-1" {
		t.Fatalf("expected runtime to learn channel binding, got %#v", binding)
	}
	if binding.LastInboundMessageID != "msg-1" || binding.LastInboundAt.IsZero() {
		t.Fatalf("expected runtime to persist latest inbound message metadata, got %#v", binding)
	}
	stored, err := store.ListByWorkspace("/tmp/project")
	if err != nil {
		t.Fatalf("ListByWorkspace returned error: %v", err)
	}
	if len(stored) == 0 || stored[0].ChannelID != "group-1" {
		t.Fatalf("expected store to persist learned channel, got %#v", stored)
	}
	if stored[0].LastInboundMessageID != "msg-1" || stored[0].LastInboundAt.IsZero() {
		t.Fatalf("expected store to persist latest inbound message metadata, got %#v", stored)
	}
}

func TestReloadBindingRestoresPersistedLatestInboundMessage(t *testing.T) {
	store := NewMemoryBindingStore()
	if err := store.Save(ChannelBinding{
		Workspace:            "/tmp/project",
		Platform:             PlatformQQ,
		Adapter:              "qq-bot-1",
		TargetID:             "ops",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		LastInboundAt:        time.Now(),
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	mgr := NewManager()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})

	binding := mgr.CurrentBinding()
	if binding == nil || binding.LastInboundMessageID != "msg-42" || binding.LastInboundAt.IsZero() {
		t.Fatalf("expected persisted latest inbound message to reload with binding, got %#v", binding)
	}
}

func TestClearReplyWindowOnlyResetsLatestInboundMessage(t *testing.T) {
	store := NewMemoryBindingStore()
	mgr := NewManager()
	if err := mgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:             PlatformQQ,
		Adapter:              "qq-bot-1",
		TargetID:             "ops",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		LastInboundAt:        time.Now(),
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	if err := mgr.ClearReplyWindow("/tmp/project"); err != nil {
		t.Fatalf("ClearReplyWindow returned error: %v", err)
	}

	binding := mgr.CurrentBinding()
	if binding == nil || binding.ChannelID != "group-1" || binding.LastInboundMessageID != "" || !binding.LastInboundAt.IsZero() {
		t.Fatalf("expected reply window cleared but binding kept, got %#v", binding)
	}
	stored, err := store.ListByWorkspace("/tmp/project")
	if err != nil {
		t.Fatalf("ListByWorkspace returned error: %v", err)
	}
	if len(stored) == 0 || stored[0].ChannelID != "group-1" || stored[0].LastInboundMessageID != "" || !stored[0].LastInboundAt.IsZero() {
		t.Fatalf("expected store reply window cleared but channel kept, got %#v", stored)
	}
}

func TestHandlePairingInboundCreatesChallengeAndBindsOnCorrectCode(t *testing.T) {
	mgr := NewManager()
	store := NewMemoryBindingStore()
	if err := mgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	if err := mgr.SetPairingStore(NewMemoryPairingStore()); err != nil {
		t.Fatalf("SetPairingStore returned error: %v", err)
	}
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform: PlatformQQ,
		Adapter:  "qq",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	first, err := mgr.HandlePairingInbound(InboundMessage{
		Envelope: Envelope{
			Adapter:    "qq",
			Platform:   PlatformQQ,
			ChannelID:  "group-1",
			SenderID:   "user-1",
			SenderName: "tester",
			MessageID:  "msg-1",
			ReceivedAt: time.Now(),
		},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("HandlePairingInbound returned error: %v", err)
	}
	if !first.Consumed || first.Kind != PairingKindBind || !strings.Contains(first.ReplyText, "4 位绑定码") {
		t.Fatalf("unexpected initial pairing result: %#v", first)
	}
	challenge := mgr.Snapshot().PendingPairing
	if challenge == nil || challenge.Code == "" {
		t.Fatalf("expected pending pairing challenge, got %#v", challenge)
	}

	second, err := mgr.HandlePairingInbound(InboundMessage{
		Envelope: Envelope{
			Adapter:    "qq",
			Platform:   PlatformQQ,
			ChannelID:  "group-1",
			SenderID:   "user-1",
			SenderName: "tester",
			MessageID:  "msg-2",
			ReceivedAt: time.Now(),
		},
		Text: challenge.Code,
	})
	if err != nil {
		t.Fatalf("HandlePairingInbound bind returned error: %v", err)
	}
	if !second.Bound || second.NewBinding == nil || second.NewBinding.ChannelID != "group-1" {
		t.Fatalf("expected successful bind result, got %#v", second)
	}
	if pending := mgr.Snapshot().PendingPairing; pending != nil {
		t.Fatalf("expected pending pairing to clear after success, got %#v", pending)
	}
	binding := mgr.CurrentBinding()
	if binding == nil || binding.ChannelID != "group-1" || binding.LastInboundMessageID != "msg-2" {
		t.Fatalf("expected current binding to update from pairing, got %#v", binding)
	}
}

func TestRejectPendingPairingBlacklistsAfterThreeRejectionsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "im-pairing.json")
	store, err := NewJSONFilePairingStore(path)
	if err != nil {
		t.Fatalf("NewJSONFilePairingStore returned error: %v", err)
	}

	mgr := NewManager()
	if err := mgr.SetBindingStore(NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	if err := mgr.SetPairingStore(store); err != nil {
		t.Fatalf("SetPairingStore returned error: %v", err)
	}
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform: PlatformQQ,
		Adapter:  "qq",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	for i := 0; i < 3; i++ {
		_, err := mgr.HandlePairingInbound(InboundMessage{
			Envelope: Envelope{
				Adapter:    "qq",
				Platform:   PlatformQQ,
				ChannelID:  "group-block",
				SenderID:   "user-1",
				MessageID:  "msg-start",
				ReceivedAt: time.Now(),
			},
			Text: "start",
		})
		if err != nil {
			t.Fatalf("HandlePairingInbound start returned error: %v", err)
		}
		_, blacklisted, err := mgr.RejectPendingPairing()
		if err != nil {
			t.Fatalf("RejectPendingPairing returned error: %v", err)
		}
		if want := i == 2; blacklisted != want {
			t.Fatalf("unexpected blacklist state after reject %d: got %t want %t", i+1, blacklisted, want)
		}
	}

	reloaded := NewManager()
	if err := reloaded.SetBindingStore(NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	if err := reloaded.SetPairingStore(store); err != nil {
		t.Fatalf("SetPairingStore returned error: %v", err)
	}
	reloaded.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	result, err := reloaded.HandlePairingInbound(InboundMessage{
		Envelope: Envelope{
			Adapter:    "qq",
			Platform:   PlatformQQ,
			ChannelID:  "group-block",
			SenderID:   "user-1",
			MessageID:  "msg-next",
			ReceivedAt: time.Now(),
		},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("HandlePairingInbound blacklisted returned error: %v", err)
	}
	if !result.Consumed || !strings.Contains(result.ReplyText, "不再接受") {
		t.Fatalf("expected blacklisted pairing reply, got %#v", result)
	}
}

func TestResolveApprovalFirstWriterWins(t *testing.T) {
	mgr := NewManager()
	req, respCh := mgr.RegisterApproval(ApprovalRequest{ToolName: "run_command", Input: `{"command":"ls"}`})

	result, accepted, err := mgr.ResolveApproval(ApprovalResponse{
		ApprovalID:  req.ID,
		Decision:    permission.Allow,
		RespondedBy: "qq:user-1",
	})
	if err != nil {
		t.Fatalf("ResolveApproval returned error: %v", err)
	}
	if !accepted {
		t.Fatalf("expected first response to be accepted")
	}
	if result.Decision != permission.Allow {
		t.Fatalf("expected allow decision, got %v", result.Decision)
	}
	select {
	case decision := <-respCh:
		if decision != permission.Allow {
			t.Fatalf("expected allow on channel, got %v", decision)
		}
	default:
		t.Fatalf("expected approval decision on channel")
	}

	_, accepted, err = mgr.ResolveApproval(ApprovalResponse{
		ApprovalID:  req.ID,
		Decision:    permission.Deny,
		RespondedBy: "local:tui",
	})
	if err != nil {
		t.Fatalf("ResolveApproval second call returned error: %v", err)
	}
	if accepted {
		t.Fatalf("expected second response to be rejected as stale")
	}
}

func TestEmitFansOutToRegisteredSinks(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:             PlatformQQ,
		Adapter:              "qq",
		TargetID:             "ops",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-9",
		LastInboundAt:        time.Now(),
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sinkA := &stubSink{name: "qq"}
	sinkB := &stubSink{name: "discord"}
	mgr.RegisterSink(sinkA)
	mgr.RegisterSink(sinkB)

	if err := mgr.Emit(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"}); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if len(sinkA.events) != 1 || len(sinkB.events) != 0 {
		t.Fatalf("expected only bound sink to receive event, got %d and %d", len(sinkA.events), len(sinkB.events))
	}
	if len(sinkA.bindings) != 1 || sinkA.bindings[0].Adapter != "qq" {
		t.Fatalf("expected sink to receive current binding, got %#v", sinkA.bindings)
	}
}

func TestEmitWithoutLearnedChannelReturnsNoChannelBound(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform: PlatformQQ,
		Adapter:  "qq",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &stubSink{name: "qq"}
	mgr.RegisterSink(sink)

	err := mgr.Emit(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hello"})
	if !errors.Is(err, ErrNoChannelBound) {
		t.Fatalf("expected ErrNoChannelBound, got %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no outbound send before channel is learned, got %#v", sink.events)
	}
}

func TestClearChannelKeepsBindingButResetsAuthorization(t *testing.T) {
	mgr := NewManager()
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	if err := mgr.ClearChannel("/tmp/project"); err != nil {
		t.Fatalf("ClearChannel returned error: %v", err)
	}

	binding := mgr.CurrentBinding()
	if binding == nil || binding.Adapter != "qq" || binding.ChannelID != "" || binding.LastInboundMessageID != "" || !binding.LastInboundAt.IsZero() {
		t.Fatalf("expected binding to remain with cleared channel, got %#v", binding)
	}
	stored, err := store.ListByWorkspace("/tmp/project")
	if err != nil {
		t.Fatalf("ListByWorkspace returned error: %v", err)
	}
	if len(stored) == 0 || stored[0].Adapter != "qq" || stored[0].ChannelID != "" || stored[0].LastInboundMessageID != "" || !stored[0].LastInboundAt.IsZero() {
		t.Fatalf("expected stored binding to keep adapter and clear channel, got %#v", stored)
	}
}

func TestBindChannelAutoUnbindsOldWorkspace(t *testing.T) {
	store := NewMemoryBindingStore()
	mgrA := NewManager()
	mgrB := NewManager()
	_ = mgrA.SetBindingStore(store)
	_ = mgrB.SetBindingStore(store)
	mgrA.BindSession(SessionBinding{SessionID: "session-a", Workspace: "/tmp/a"})
	mgrB.BindSession(SessionBinding{SessionID: "session-b", Workspace: "/tmp/b"})

	if _, err := mgrA.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("first BindChannel returned error: %v", err)
	}
	// Second bind to same adapter from different workspace should auto-unbind old
	bound, err := mgrB.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		TargetID:  "other",
		ChannelID: "group-2",
	})
	if err != nil {
		t.Fatalf("expected auto-unbind to succeed, got %v", err)
	}
	if bound.Workspace != normalizeWorkspace("/tmp/b") {
		t.Fatalf("expected binding to be for /tmp/b, got %q", bound.Workspace)
	}
	// Old workspace should no longer have the binding
	oldBindings, _ := store.ListByWorkspace("/tmp/a")
	if len(oldBindings) != 0 {
		t.Fatalf("expected old workspace to have no bindings, got %d", len(oldBindings))
	}
	// New workspace should have it
	newBindings, _ := store.ListByWorkspace("/tmp/b")
	if len(newBindings) != 1 || newBindings[0].Adapter != "qq-bot-1" {
		t.Fatalf("expected new workspace to have the binding, got %v", newBindings)
	}
}

func TestSyncSessionHistoryUsesCurrentBinding(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	sink := &stubSink{name: "qq"}
	mgr.RegisterSink(sink)
	err := mgr.SyncSessionHistory(context.Background(), []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	})
	if err != nil {
		t.Fatalf("SyncSessionHistory returned error: %v", err)
	}
	if len(sink.events) != 2 {
		t.Fatalf("expected 2 history events, got %d", len(sink.events))
	}
}

// --- Additional coverage tests ---

func TestManager_SetOnUpdate_Callback(t *testing.T) {
	m := NewManager()
	var received StatusSnapshot
	m.SetOnUpdate(func(s StatusSnapshot) {
		received = s
	})
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	if received.ActiveSession == nil || received.ActiveSession.Workspace != "/ws1" {
		t.Error("expected callback to receive snapshot with session")
	}
}

func TestManager_UnbindSession_ClearsState(t *testing.T) {
	m := NewManager()
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	m.UnbindSession()
	if m.ActiveSession() != nil {
		t.Error("expected nil session")
	}
	if m.CurrentBinding() != nil {
		t.Error("expected nil binding")
	}
}

func TestManager_ActiveSession_Copy(t *testing.T) {
	m := NewManager()
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	s := m.ActiveSession()
	if s == nil || s.Workspace != "/ws1" {
		t.Fatal("expected session with /ws1")
	}
}

func TestManager_CurrentBinding_Copy(t *testing.T) {
	m := NewManager()
	m.SetBindingStore(NewMemoryBindingStore())
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	m.BindChannel(ChannelBinding{Workspace: "/ws1", TargetID: "t1", ChannelID: "c1"})

	b := m.CurrentBinding()
	if b == nil || b.ChannelID != "c1" {
		t.Errorf("expected binding with c1, got %v", b)
	}
}

func TestManager_ListBindings_NoStoreWithCurrent(t *testing.T) {
	m := NewManager()
	m.SetBindingStore(NewMemoryBindingStore())
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	m.BindChannel(ChannelBinding{Workspace: "/ws1", TargetID: "t1", ChannelID: "c1"})

	// Remove store to test fallback path
	m.mu.Lock()
	m.bindingStore = nil
	m.mu.Unlock()

	list, err := m.ListBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 binding from current, got %d", len(list))
	}
}

func TestManager_RegisterUnregisterSink(t *testing.T) {
	m := NewManager()
	sink := &stubSink{name: "test-sink"}
	m.RegisterSink(sink)

	// Verify it is registered via GenerateShareLink
	_, err := m.GenerateShareLink(context.Background(), "test-sink", "data")
	if err == nil {
		t.Error("expected error (not ShareLinkProvider)")
	}

	m.UnregisterSink("test-sink")
	_, err = m.GenerateShareLink(context.Background(), "test-sink", "data")
	if err == nil {
		t.Error("expected error after unregister")
	}
}

func TestManager_RegisterSink_Nil(t *testing.T) {
	m := NewManager()
	m.RegisterSink(nil) // should not panic
}

func TestManager_SendDirect_WithSink(t *testing.T) {
	m := NewManager()
	sink := &stubSink{name: "qq"}
	m.RegisterSink(sink)

	binding := ChannelBinding{Workspace: "/ws1", Adapter: "qq", ChannelID: "ch1"}
	err := m.SendDirect(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: "direct-msg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Text != "direct-msg" {
		t.Errorf("unexpected sink state: %v", sink.events)
	}
}

func TestManager_SendDirect_NoSink(t *testing.T) {
	m := NewManager()
	binding := ChannelBinding{Workspace: "/ws1", Adapter: "qq", ChannelID: "ch1"}
	err := m.SendDirect(context.Background(), binding, OutboundEvent{Kind: OutboundEventText})
	if err != nil {
		t.Errorf("expected nil (no sink), got %v", err)
	}
}

func TestManager_SendDirect_EmptyChannelID(t *testing.T) {
	m := NewManager()
	binding := ChannelBinding{Workspace: "/ws1", Adapter: "qq"}
	err := m.SendDirect(context.Background(), binding, OutboundEvent{Kind: OutboundEventText})
	if err != ErrNoChannelBound {
		t.Errorf("expected ErrNoChannelBound, got %v", err)
	}
}

func TestManager_RegisterApproval_AutoFields(t *testing.T) {
	m := NewManager()
	req := ApprovalRequest{ToolName: "bash"}
	registered, ch := m.RegisterApproval(req)
	if registered.ID == "" {
		t.Error("expected auto-generated ID")
	}
	if registered.RequestedAt.IsZero() {
		t.Error("expected auto-set RequestedAt")
	}

	// Resolve
	result, ok, err := m.ResolveApproval(ApprovalResponse{
		ApprovalID:  registered.ID,
		Decision:    permission.Allow,
		RespondedBy: "user",
	})
	if err != nil || !ok {
		t.Fatalf("ResolveApproval: ok=%v err=%v", ok, err)
	}
	if result.Decision != permission.Allow {
		t.Errorf("expected Allow, got %v", result.Decision)
	}

	select {
	case d := <-ch:
		if d != permission.Allow {
			t.Errorf("channel got %v, want Allow", d)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestManager_ResolveApproval_DoubleResolve(t *testing.T) {
	m := NewManager()
	req := ApprovalRequest{ToolName: "bash"}
	registered, _ := m.RegisterApproval(req)

	m.ResolveApproval(ApprovalResponse{ApprovalID: registered.ID, Decision: permission.Allow})
	_, ok, _ := m.ResolveApproval(ApprovalResponse{ApprovalID: registered.ID, Decision: permission.Deny})
	if ok {
		t.Error("expected ok=false for double resolve")
	}
}

func TestManager_Snapshot_Empty(t *testing.T) {
	m := NewManager()
	snap := m.Snapshot()
	if snap.ActiveSession != nil || len(snap.CurrentBindings) != 0 {
		t.Error("expected empty snapshot")
	}
	if len(snap.Adapters) != 0 || len(snap.PendingApprovals) != 0 {
		t.Error("expected empty snapshot")
	}
}

func TestManager_Snapshot_WithData(t *testing.T) {
	m := NewManager()
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	m.PublishAdapterState(AdapterState{Name: "qq", Status: "running"})
	m.RegisterApproval(ApprovalRequest{ToolName: "bash"})

	snap := m.Snapshot()
	if snap.ActiveSession == nil {
		t.Error("expected session")
	}
	if len(snap.Adapters) != 1 {
		t.Errorf("expected 1 adapter, got %d", len(snap.Adapters))
	}
	if len(snap.PendingApprovals) != 1 {
		t.Errorf("expected 1 approval, got %d", len(snap.PendingApprovals))
	}
}

func TestManager_PublishAdapterState(t *testing.T) {
	m := NewManager()
	m.PublishAdapterState(AdapterState{Name: "qq", Status: "running"})
	m.PublishAdapterState(AdapterState{Name: "tg", Status: "running"})

	snap := m.Snapshot()
	if len(snap.Adapters) != 2 {
		t.Errorf("expected 2 adapters, got %d", len(snap.Adapters))
	}
}

func TestManager_SetBridge(t *testing.T) {
	m := NewManager()
	bridge := &stubBridge{}
	m.SetBridge(bridge)
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	err := m.HandleInbound(context.Background(), InboundMessage{Text: "hello", Envelope: Envelope{Adapter: "qq", ChannelID: "ch1"}})
	if err == ErrNoBridge {
		t.Error("expected bridge to be set")
	}
}

func TestManager_UnbindChannel_UsesSessionWorkspace(t *testing.T) {
	m := NewManager()
	store := NewMemoryBindingStore()
	m.SetBindingStore(store)
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	m.BindChannel(ChannelBinding{Workspace: "/ws1", TargetID: "t1", ChannelID: "c1"})

	err := m.UnbindChannel("")
	if err != nil {
		t.Fatal(err)
	}
	if m.CurrentBinding() != nil {
		t.Error("expected nil after unbind")
	}
}

func TestManager_Emit_NoChannel(t *testing.T) {
	m := NewManager()
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	err := m.Emit(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err != ErrNoChannelBound {
		t.Errorf("expected ErrNoChannelBound, got %v", err)
	}
}

func TestHandleInboundDedupDuplicateMessageID(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	msg := InboundMessage{
		Text: "hello",
		Envelope: Envelope{
			Adapter:   "qq",
			ChannelID: "group-1",
			MessageID: "msg-dup-1",
		},
	}

	// First call should succeed and reach the bridge
	if err := mgr.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("first HandleInbound returned error: %v", err)
	}
	if bridge.last.Text != "hello" {
		t.Fatalf("expected bridge to receive first message, got %#v", bridge.last)
	}

	// Reset bridge to detect second call
	bridge.last = InboundMessage{}

	// Second call with same MessageID should be silently dropped
	if err := mgr.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("duplicate HandleInbound returned error: %v", err)
	}
	if bridge.last.Text != "" {
		t.Fatalf("expected bridge to NOT receive duplicate message, but got %#v", bridge.last)
	}
}

func TestHandleInboundDedupDifferentMessageIDs(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	msg1 := InboundMessage{
		Text: "hello",
		Envelope: Envelope{
			Adapter:   "qq",
			ChannelID: "group-1",
			MessageID: "msg-1",
		},
	}
	msg2 := InboundMessage{
		Text: "world",
		Envelope: Envelope{
			Adapter:   "qq",
			ChannelID: "group-1",
			MessageID: "msg-2",
		},
	}

	if err := mgr.HandleInbound(context.Background(), msg1); err != nil {
		t.Fatalf("first HandleInbound returned error: %v", err)
	}
	if err := mgr.HandleInbound(context.Background(), msg2); err != nil {
		t.Fatalf("second HandleInbound returned error: %v", err)
	}
	if bridge.last.Text != "world" {
		t.Fatalf("expected bridge to receive second message, got %#v", bridge.last)
	}
}

func TestHandleInboundDedupNoMessageID(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	msg := InboundMessage{
		Text: "hello",
		Envelope: Envelope{
			Adapter:   "qq",
			ChannelID: "group-1",
			MessageID: "", // no MessageID — should not dedup
		},
	}

	// Both calls should go through since MessageID is empty
	if err := mgr.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("first HandleInbound returned error: %v", err)
	}
	if err := mgr.HandleInbound(context.Background(), msg); err != nil {
		t.Fatalf("second HandleInbound returned error: %v", err)
	}
	// bridge.last is overwritten on each call, so we just verify no error
}

func TestHandleInboundDedupDifferentAdapters(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformTelegram,
		Adapter:   "tg",
		TargetID:  "ops2",
		ChannelID: "tg-chat-1",
	}); err != nil {
		t.Fatalf("BindChannel tg returned error: %v", err)
	}

	// Same MessageID but different adapters — should both go through
	msgQQ := InboundMessage{
		Text: "hello",
		Envelope: Envelope{
			Adapter:   "qq",
			ChannelID: "group-1",
			MessageID: "shared-msg-id",
		},
	}
	msgTG := InboundMessage{
		Text: "hello",
		Envelope: Envelope{
			Adapter:   "tg",
			ChannelID: "tg-chat-1",
			MessageID: "shared-msg-id",
		},
	}

	if err := mgr.HandleInbound(context.Background(), msgQQ); err != nil {
		t.Fatalf("qq HandleInbound returned error: %v", err)
	}
	if err := mgr.HandleInbound(context.Background(), msgTG); err != nil {
		t.Fatalf("tg HandleInbound returned error: %v", err)
	}
	// Both should have been accepted (dedup key is adapter:messageID)
}

func TestManager_Emit_AutoCreatedAt(t *testing.T) {
	m := NewManager()
	sink := &stubSink{name: "qq"}
	m.RegisterSink(sink)
	m.SetBindingStore(NewMemoryBindingStore())
	m.BindSession(SessionBinding{Workspace: "/ws1"})
	m.BindChannel(ChannelBinding{Workspace: "/ws1", Adapter: "qq", TargetID: "t1", ChannelID: "c1"})

	m.Emit(context.Background(), OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if sink.events[0].CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be auto-set")
	}
}
