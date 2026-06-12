//go:build integration_service

package im

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// newE2EAgent creates a minimal agent for testing (no tools, 3 iterations max).
func newE2EAgent(t *testing.T, prov provider.Provider) *agent.Agent {
	t.Helper()
	registry := tool.NewRegistry()
	return agent.NewAgent(prov, registry, "You are a test assistant. Respond with exactly one word.", 3)
}

// ============================================================
// SubmitInboundMessage — full agent submission path
// ============================================================

// TestE2ESubmitInboundMessage_AgentRun verifies that a regular text message
// starts an agent run and the agent responds.
func TestE2ESubmitInboundMessage_AgentRun(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("set GGCODE_E2E=1 to run")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("no config available")
	}
	prov := resolveE2EProvider(t, cfg)
	if prov == nil {
		t.Skip("no provider available")
	}
	ag := newE2EAgent(t, prov)

	mgr := NewManager()
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, ag, emitter, nil, nil)

	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq", Platform: PlatformQQ},
		Text:     "Say hello.",
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage error: %v", err)
	}

	// Wait for agent to finish (max 30s)
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bridge.mu.Lock()
			busy := bridge.cancelFunc != nil
			bridge.mu.Unlock()
			if !busy {
				if qq.eventCount() == 0 {
					t.Error("expected at least one event on qq after agent run")
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for agent run to complete")
		}
	}
}

// ============================================================
// SubmitInboundMessage — interruption queuing
// ============================================================

// TestE2ESubmitInboundMessage_InterruptionQueue verifies that a second message
// while the agent is running is queued as an interruption.
func TestE2ESubmitInboundMessage_InterruptionQueue(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("set GGCODE_E2E=1 to run")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("no config available")
	}
	prov := resolveE2EProvider(t, cfg)
	if prov == nil {
		t.Skip("no provider available")
	}
	ag := newE2EAgent(t, prov)

	mgr := NewManager()
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, ag, emitter, nil, nil)

	// First message starts agent run
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq", Platform: PlatformQQ},
		Text:     "Count from 1 to 5.",
	})
	if err != nil {
		t.Fatalf("first SubmitInboundMessage error: %v", err)
	}

	// Wait until agent is running
	time.Sleep(1 * time.Second)

	bridge.mu.Lock()
	busy := bridge.cancelFunc != nil
	bridge.mu.Unlock()
	if !busy {
		t.Fatal("expected agent to be running after first message")
	}

	// Second message should be queued as interruption
	err = bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq", Platform: PlatformQQ},
		Text:     "Now count from 6 to 10.",
	})
	if err != nil {
		t.Fatalf("second SubmitInboundMessage error: %v", err)
	}

	bridge.mu.Lock()
	queueLen := len(bridge.pendingInterruptions)
	bridge.mu.Unlock()
	if queueLen == 0 {
		t.Error("expected interruption to be queued")
	}

	// Wait for both runs to finish
	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bridge.mu.Lock()
			busy := bridge.cancelFunc != nil
			bridge.mu.Unlock()
			if !busy {
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for agent runs to complete")
		}
	}
}

// ============================================================
// HandleAskUser — blocking wait + button callback
// ============================================================

// TestE2EHandleAskUser_InteractiveCallback verifies the full cycle:
// HandleAskUser blocks → interactive button sent → callback arrives → answer returned.
func TestE2EHandleAskUser_InteractiveCallback(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("set GGCODE_E2E=1 to run")
	}

	mgr := NewManager()
	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		bridge.handleInteractiveCallback(cb)
	})

	var resp toolpkg.AskUserResponse
	var respErr error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		resp, respErr = bridge.HandleAskUser(context.Background(), toolpkg.AskUserRequest{
			Title: "Choose",
			Questions: []toolpkg.AskUserQuestion{
				{
					ID:      "q1",
					Title:   "Pick one",
					Kind:    toolpkg.AskUserKindSingle,
					Choices: []toolpkg.AskUserChoice{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
				},
			},
		})
	}()

	time.Sleep(500 * time.Millisecond)

	last := tg.lastInteractive()
	if last == nil {
		t.Fatal("expected interactive message to be sent to tg")
	}
	if len(last.Buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(last.Buttons))
	}

	mgr.HandleInteractiveCallback(InteractiveCallback{
		Values: []string{"yes"},
	})

	wg.Wait()

	if respErr != nil {
		t.Fatalf("HandleAskUser error: %v", respErr)
	}
	if len(resp.Answers) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answers))
	}
	if resp.Answers[0].SelectedChoices[0] != "Yes" {
		t.Errorf("expected 'Yes', got %q", resp.Answers[0].SelectedChoices[0])
	}
}

// TestE2EHandleAskUser_TextReply verifies HandleAskUser with text reply
// from a non-interactive adapter.
func TestE2EHandleAskUser_TextReply(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("set GGCODE_E2E=1 to run")
	}

	mgr := NewManager()
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	var resp toolpkg.AskUserResponse
	var respErr error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		resp, respErr = bridge.HandleAskUser(context.Background(), toolpkg.AskUserRequest{
			Title: "Name",
			Questions: []toolpkg.AskUserQuestion{
				{ID: "q1", Title: "What is your name?", Kind: toolpkg.AskUserKindText},
			},
		})
	}()

	time.Sleep(500 * time.Millisecond)

	if qq.eventCount() == 0 {
		t.Fatal("expected text question to be sent to qq")
	}

	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq", Platform: PlatformQQ},
		Text:     "Alice",
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage error: %v", err)
	}

	wg.Wait()

	if respErr != nil {
		t.Fatalf("HandleAskUser error: %v", respErr)
	}
	if len(resp.Answers) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answers))
	}
	if resp.Answers[0].FreeformText != "Alice" {
		t.Errorf("expected 'Alice', got %q", resp.Answers[0].FreeformText)
	}
}

// TestE2EHandleAskUser_MultiQuestion verifies multi-question ask_user
// with mixed question types (choice + text).
func TestE2EHandleAskUser_MultiQuestion(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("set GGCODE_E2E=1 to run")
	}

	mgr := NewManager()
	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		bridge.handleInteractiveCallback(cb)
	})

	var resp toolpkg.AskUserResponse
	var respErr error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		resp, respErr = bridge.HandleAskUser(context.Background(), toolpkg.AskUserRequest{
			Title: "Setup",
			Questions: []toolpkg.AskUserQuestion{
				{
					ID:      "q1",
					Title:   "Language",
					Kind:    toolpkg.AskUserKindSingle,
					Choices: []toolpkg.AskUserChoice{{ID: "go", Label: "Go"}, {ID: "rust", Label: "Rust"}},
				},
				{
					ID:    "q2",
					Title: "Project name?",
					Kind:  toolpkg.AskUserKindText,
				},
			},
		})
	}()

	// Wait for first question (choice with interactive buttons)
	time.Sleep(500 * time.Millisecond)

	if tg.lastInteractive() == nil {
		t.Fatal("expected interactive message for first question")
	}

	// Answer first question via button callback
	mgr.HandleInteractiveCallback(InteractiveCallback{
		Values: []string{"go"},
	})

	// Wait for second question (text)
	time.Sleep(500 * time.Millisecond)

	// Answer second question via text
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{Adapter: "qq", Platform: PlatformQQ},
		Text:     "myproject",
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage error: %v", err)
	}

	wg.Wait()

	if respErr != nil {
		t.Fatalf("HandleAskUser error: %v", respErr)
	}
	if resp.QuestionCount != 2 {
		t.Errorf("expected 2 questions, got %d", resp.QuestionCount)
	}
	if resp.AnsweredCount != 2 {
		t.Errorf("expected 2 answered, got %d", resp.AnsweredCount)
	}
	if resp.Answers[0].SelectedChoices[0] != "Go" {
		t.Errorf("q1 answer: %v", resp.Answers[0].SelectedChoices)
	}
	if resp.Answers[1].FreeformText != "myproject" {
		t.Errorf("q2 answer: %q", resp.Answers[1].FreeformText)
	}
}

// TestE2EHandleAskUser_ContextCancel verifies HandleAskUser returns error
// when context is cancelled while waiting.
func TestE2EHandleAskUser_ContextCancel(t *testing.T) {
	mgr := NewManager()
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	var respErr error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		_, respErr = bridge.HandleAskUser(ctx, toolpkg.AskUserRequest{
			Title: "Stuck",
			Questions: []toolpkg.AskUserQuestion{
				{ID: "q1", Title: "Never answered", Kind: toolpkg.AskUserKindText},
			},
		})
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()
	wg.Wait()

	if respErr == nil {
		t.Fatal("expected error from cancelled context")
	}
	if respErr != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", respErr)
	}

	bridge.mu.Lock()
	pending := bridge.pendingAsk
	bridge.mu.Unlock()
	if pending != nil {
		t.Error("pendingAsk should be nil after context cancel")
	}
}

// ============================================================
// handleInteractiveCallback — multi-select
// ============================================================

// TestE2EHandleInteractiveCallback_MultiSelect verifies multi-select toggle
// and __done__ submission.
func TestE2EHandleInteractiveCallback_MultiSelect(t *testing.T) {
	mgr := NewManager()
	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		bridge.handleInteractiveCallback(cb)
	})

	var resp toolpkg.AskUserResponse
	var respErr error
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		resp, respErr = bridge.HandleAskUser(context.Background(), toolpkg.AskUserRequest{
			Title: "Pick",
			Questions: []toolpkg.AskUserQuestion{
				{
					ID:      "q1",
					Title:   "Choose several",
					Kind:    toolpkg.AskUserKindMulti,
					Choices: []toolpkg.AskUserChoice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}, {ID: "c", Label: "C"}},
				},
			},
		})
	}()

	time.Sleep(200 * time.Millisecond)

	// Toggle A on
	mgr.HandleInteractiveCallback(InteractiveCallback{Values: []string{"a"}})
	time.Sleep(100 * time.Millisecond)

	// Toggle B on
	mgr.HandleInteractiveCallback(InteractiveCallback{Values: []string{"b"}})
	time.Sleep(100 * time.Millisecond)

	// Toggle A off
	mgr.HandleInteractiveCallback(InteractiveCallback{Values: []string{"a"}})
	time.Sleep(100 * time.Millisecond)

	// Submit
	mgr.HandleInteractiveCallback(InteractiveCallback{Values: []string{"__done__"}})

	wg.Wait()

	if respErr != nil {
		t.Fatalf("HandleAskUser error: %v", respErr)
	}
	selected := resp.Answers[0].SelectedChoiceIDs
	if len(selected) != 1 || selected[0] != "b" {
		t.Errorf("expected only 'b' selected, got %v", selected)
	}
}

// ============================================================
// ConsumeRestartDebug
// ============================================================

func TestE2EConsumeRestartDebug(t *testing.T) {
	mgr := NewManager()
	bridge := NewDaemonBridge(mgr, nil, NewIMEmitter(mgr, "en", "/ws"), nil, nil)

	if bridge.ConsumeRestartDebug() {
		t.Error("expected false initially")
	}

	bridge.mu.Lock()
	bridge.restartDebug = true
	bridge.mu.Unlock()

	if !bridge.ConsumeRestartDebug() {
		t.Error("expected true after setting")
	}

	if bridge.ConsumeRestartDebug() {
		t.Error("expected false after consume")
	}
}

// ============================================================
// Subscribe
// ============================================================

// TestE2EBridge_Subscribe verifies event subscription works via broadcastEvent.
func TestE2EBridge_Subscribe(t *testing.T) {
	mgr := NewManager()
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	received := make(chan provider.StreamEvent, 10)
	unsub := bridge.Subscribe(func(evt provider.StreamEvent) {
		received <- evt
	})
	if unsub == nil {
		t.Fatal("expected non-nil unsubscribe function")
	}

	// Broadcast an event directly
	bridge.broadcastEvent(provider.StreamEvent{
		Type: provider.StreamEventText,
		Text: "hello subscriber",
	})

	select {
	case evt := <-received:
		if evt.Text != "hello subscriber" {
			t.Errorf("expected 'hello subscriber', got %q", evt.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscriber event")
	}

	unsub()
}
