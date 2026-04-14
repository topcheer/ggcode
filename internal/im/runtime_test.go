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
	stored, err := store.Load("/tmp/project")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if stored == nil || stored.ChannelID != "group-1" {
		t.Fatalf("expected store to persist learned channel, got %#v", stored)
	}
	if stored.LastInboundMessageID != "msg-1" || stored.LastInboundAt.IsZero() {
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
	stored, err := store.Load("/tmp/project")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if stored == nil || stored.ChannelID != "group-1" || stored.LastInboundMessageID != "" || !stored.LastInboundAt.IsZero() {
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
	stored, err := store.Load("/tmp/project")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if stored == nil || stored.Adapter != "qq" || stored.ChannelID != "" || stored.LastInboundMessageID != "" || !stored.LastInboundAt.IsZero() {
		t.Fatalf("expected stored binding to keep adapter and clear channel, got %#v", stored)
	}
}

func TestBindChannelEnforcesAdapterExclusivity(t *testing.T) {
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
	if _, err := mgrB.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq-bot-1",
		TargetID:  "other",
		ChannelID: "group-2",
	}); !errors.Is(err, ErrAdapterAlreadyBound) {
		t.Fatalf("expected ErrAdapterAlreadyBound, got %v", err)
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
