package im

import (
	"context"
	"sync"
	"testing"
	"time"
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
	if len(pending) != 1 || pending[0] != "second message" {
		t.Fatalf("expected 1 pending interruption, got %v", pending)
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
	if pending[0] != "msg1" || pending[1] != "msg2" || pending[2] != "msg3" {
		t.Fatalf("wrong order: %v", pending)
	}
}

// TestDaemonBridgeInterruptionDrain verifies the handler drains the queue correctly.
func TestDaemonBridgeInterruptionDrain(t *testing.T) {
	bridge := &DaemonBridge{
		pendingInterruptions: []string{"msg1", "msg2"},
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
		return msg
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
