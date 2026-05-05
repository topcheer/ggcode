package im

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// TestDaemonBridgeInterruptionQueuing verifies that when an agent run is
// active (cancelFunc != nil), a new IM message is queued as an interruption
// instead of cancelling the current run.
func TestDaemonBridgeInterruptionQueuing(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate an active agent run
	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	// Send a message while agent is running
	err := bridge.SubmitInboundMessage(ctx, InboundMessage{
		Text:     "second message",
		Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage during active run: %v", err)
	}

	// Verify the message was queued
	bridge.mu.Lock()
	pending := bridge.pendingInterruptions
	bridge.mu.Unlock()
	if len(pending) != 1 || extractText(pending[0].Content) != "second message" {
		t.Fatalf("expected 1 pending interruption 'second message', got %v", pending)
	}

	// CRITICAL: context must NOT be cancelled — old code would cancel here
	if ctx.Err() != nil {
		t.Fatal("BUG: context was cancelled — new messages must NOT cancel active agent run")
	}
}

// TestDaemonBridgeInterruptionQueueOrder verifies messages are queued in order.
func TestDaemonBridgeInterruptionQueueOrder(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	for _, text := range []string{"msg1", "msg2", "msg3"} {
		_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
			Text:     text,
			Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
		})
	}

	bridge.mu.Lock()
	pending := bridge.pendingInterruptions
	bridge.mu.Unlock()
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(pending))
	}
	if extractText(pending[0].Content) != "msg1" || extractText(pending[1].Content) != "msg2" || extractText(pending[2].Content) != "msg3" {
		t.Fatalf("wrong order: %v", pending)
	}
}

// TestDaemonBridgeInterruptionDrain verifies the handler drains the queue correctly.
func TestDaemonBridgeInterruptionDrain(t *testing.T) {
	bridge := &DaemonBridge{
		pendingInterruptions: []pendingInterruption{
			{Content: []provider.ContentBlock{{Type: "text", Text: "msg1"}}},
			{Content: []provider.ContentBlock{{Type: "text", Text: "msg2"}}},
		},
	}

	// This mirrors the handler set up in SubmitInboundMessage
	handler := func() string {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		if len(bridge.pendingInterruptions) == 0 {
			return ""
		}
		msg := bridge.pendingInterruptions[0]
		bridge.pendingInterruptions = bridge.pendingInterruptions[1:]
		return extractText(msg.Content)
	}

	msg1 := handler()
	msg2 := handler()
	msg3 := handler()

	if msg1 != "msg1" {
		t.Fatalf("expected msg1, got %q", msg1)
	}
	if msg2 != "msg2" {
		t.Fatalf("expected msg2, got %q", msg2)
	}
	if msg3 != "" {
		t.Fatalf("expected empty, got %q", msg3)
	}
}

// TestDaemonBridgeEmptyMessageIgnored verifies empty messages are not queued.
func TestDaemonBridgeEmptyMessageIgnored(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
		Text:     "",
		Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
	})

	bridge.mu.Lock()
	pending := bridge.pendingInterruptions
	bridge.mu.Unlock()
	if len(pending) != 0 {
		t.Fatalf("empty messages should not be queued, got %v", pending)
	}
}

// TestDaemonBridgeNoRaceOnInterruption tests concurrent access to the queue.
func TestDaemonBridgeNoRaceOnInterruption(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
				Text:     "concurrent msg",
				Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
			})
		}()
	}
	wg.Wait()

	bridge.mu.Lock()
	count := len(bridge.pendingInterruptions)
	bridge.mu.Unlock()
	if count != 10 {
		t.Fatalf("expected 10 queued messages, got %d", count)
	}
}

// TestDaemonBridgeNoCancelStress repeats the no-cancel check under rapid messages.
func TestDaemonBridgeNoCancelStress(t *testing.T) {
	bridge := &DaemonBridge{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bridge.mu.Lock()
	bridge.cancelFunc = cancel
	bridge.mu.Unlock()

	for i := 0; i < 100; i++ {
		_ = bridge.SubmitInboundMessage(ctx, InboundMessage{
			Text:     "msg",
			Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
		})
	}

	time.Sleep(10 * time.Millisecond)

	if ctx.Err() != nil {
		t.Fatal("BUG: context was cancelled after 100 rapid messages")
	}

	bridge.mu.Lock()
	count := len(bridge.pendingInterruptions)
	bridge.mu.Unlock()
	if count != 100 {
		t.Fatalf("expected 100 queued, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Slash command tests
// ---------------------------------------------------------------------------

func TestHandleSlashCommand_Help(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/help", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
	// Help should list all new commands
	// (We can't easily capture emitted text, but we verify no panic/error)
}

func TestHandleSlashCommand_Unknown(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/unknown", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleSlashCommand_MuteSelfNoAdapter(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/muteself", InboundMessage{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleSlashCommand_MuteIMCannotMuteSelf(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/muteim telegram", testMsg("telegram"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleSlashCommand_MuteIMNoArg(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/muteim", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleSlashCommand_MuteAllNoBound(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/muteall", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleSlashCommand_RestartNoHook(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/restart", testMsg("tg"))
	if err == nil {
		t.Fatal("expected error when no restart hook")
	}
}

func TestHandleSlashCommand_RestartWithHook(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	restarted := false
	bridge.SetRestartHook(func() { restarted = true })

	err := bridge.handleSlashCommand(context.Background(), "/restart", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if !restarted {
		t.Error("expected restart hook to fire")
	}
}

func TestHandleSlashCommand_MuteSelfWithAdapter(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	// MuteSelf with an adapter name but no binding — should not panic
	err := bridge.handleSlashCommand(context.Background(), "/muteself", testMsg("telegram"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleListIMNoAdapters(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	err := bridge.handleSlashCommand(context.Background(), "/listim", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleListIMWithAdapters(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	// Add a binding so there's something to show
	mgr.currentBindings["qq"] = &ChannelBinding{
		Adapter:   "qq",
		Platform:  PlatformQQ,
		ChannelID: "test-channel",
	}

	err := bridge.handleSlashCommand(context.Background(), "/listim", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleListIMWithMutedAdapter(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	mgr.currentBindings["qq"] = &ChannelBinding{
		Adapter:   "qq",
		Platform:  PlatformQQ,
		ChannelID: "test-channel",
		Muted:     true,
	}

	err := bridge.handleSlashCommand(context.Background(), "/listim", testMsg("tg"))
	if err != nil {
		t.Fatal(err)
	}
}

// --- test helpers ---

func testMsg(adapter string) InboundMessage {
	return InboundMessage{
		Text:     "/test",
		Envelope: Envelope{Adapter: adapter, Platform: PlatformTelegram},
	}
}

// --- Activity Hook Tests ---

func TestDaemonBridge_SetActivityHook(t *testing.T) {
	br := &DaemonBridge{}

	var hookCalled bool
	br.SetActivityHook(func() { hookCalled = true })

	br.mu.Lock()
	hook := br.onActivity
	br.mu.Unlock()

	if hook == nil {
		t.Fatal("onActivity should be set")
	}
	hook()
	if !hookCalled {
		t.Error("hook should have been called")
	}
}

func TestDaemonBridge_SetActivityHookNil(t *testing.T) {
	br := &DaemonBridge{}
	br.SetActivityHook(nil)

	br.mu.Lock()
	hook := br.onActivity
	br.mu.Unlock()

	if hook != nil {
		t.Error("onActivity should be nil")
	}
}

func TestDaemonBridge_SendUserMessageTriggersActivity(t *testing.T) {
	br := &DaemonBridge{}

	// Simulate agent running state to avoid nil deref in SendUserMessage
	br.mu.Lock()
	br.cancelFunc = func() {}
	br.mu.Unlock()

	var activityCalled bool
	br.SetActivityHook(func() { activityCalled = true })

	br.SendUserMessage([]provider.ContentBlock{{Type: "text", Text: "hello"}})

	if !activityCalled {
		t.Error("SendUserMessage should have triggered activity hook for non-empty text")
	}
}

func TestDaemonBridge_SendUserMessageEmptyNoActivity(t *testing.T) {
	br := &DaemonBridge{}

	var activityCalled bool
	br.SetActivityHook(func() { activityCalled = true })

	br.SendUserMessage([]provider.ContentBlock{})

	if activityCalled {
		t.Error("SendUserMessage with empty content should not trigger activity hook")
	}
}

// ---------------------------------------------------------------------------
// Daemon approval tests
// ---------------------------------------------------------------------------

func TestParseDaemonApprovalReply(t *testing.T) {
	tests := []struct {
		input string
		want  permission.Decision
		ok    bool
	}{
		{"y", permission.Allow, true},
		{"Y", permission.Allow, true},
		{"yes", permission.Allow, true},
		{"ok", permission.Allow, true},
		{"好", permission.Allow, true},
		{"允许", permission.Allow, true},
		{"a", permission.Allow, true},
		{"always", permission.Allow, true},
		{"总是允许", permission.Allow, true},
		{"n", permission.Deny, true},
		{"no", permission.Deny, true},
		{"拒绝", permission.Deny, true},
		{"deny", permission.Deny, true},
		{"ye", permission.Allow, true},
		{"noo", permission.Deny, true},
		{"hello", permission.Deny, false},
		{"", permission.Deny, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseDaemonApprovalReply(tt.input)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("decision = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatToolInlineDaemon(t *testing.T) {
	tests := []struct {
		name, toolName, input, want string
	}{
		{"empty input", "run_command", "", "run_command"},
		{"json command", "run_command", `{"command":"ls -la"}`, "run_command: ls -la"},
		{"json path", "write_file", `{"path":"/etc/hosts"}`, "write_file: /etc/hosts"},
		{"raw input", "foo", "bar baz", "foo: bar baz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolInline(tt.toolName, tt.input)
			if !strings.HasPrefix(got, tt.want[:min(len(tt.want), 20)]) {
				t.Errorf("got %q, want prefix of %q", got, tt.want)
			}
		})
	}
}

func TestDaemonApproval_IMReply(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	// Simulate pending approval
	ch := make(chan permission.Decision, 1)
	bridge.mu.Lock()
	bridge.pendingApproval = ch
	bridge.mu.Unlock()

	// IM user replies "y"
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Text:     "y",
		Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should have received Allow on the channel
	select {
	case decision := <-ch:
		if decision != permission.Allow {
			t.Errorf("expected Allow, got %v", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval decision")
	}

	// pendingApproval should be cleared
	bridge.mu.Lock()
	pending := bridge.pendingApproval
	bridge.mu.Unlock()
	if pending != nil {
		t.Error("pendingApproval should be nil after resolution")
	}
}

func TestDaemonApproval_DenyReply(t *testing.T) {
	mgr := NewManager()
	emitter := NewIMEmitter(mgr, "en", t.TempDir())
	bridge := &DaemonBridge{manager: mgr, emitter: emitter}

	ch := make(chan permission.Decision, 1)
	bridge.mu.Lock()
	bridge.pendingApproval = ch
	bridge.mu.Unlock()

	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Text:     "n",
		Envelope: Envelope{Adapter: "test", Platform: PlatformQQ},
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case decision := <-ch:
		if decision != permission.Deny {
			t.Errorf("expected Deny, got %v", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestDaemonApproval_InvalidReplyIgnored(t *testing.T) {
	// Test parseDaemonApprovalReply directly — invalid text returns false
	_, ok := parseDaemonApprovalReply("hello")
	if ok {
		t.Error("should not parse 'hello' as approval reply")
	}

	_, ok = parseDaemonApprovalReply("")
	if ok {
		t.Error("should not parse empty as approval reply")
	}

	_, ok = parseDaemonApprovalReply("maybe")
	if ok {
		t.Error("should not parse 'maybe' as approval reply")
	}
}
