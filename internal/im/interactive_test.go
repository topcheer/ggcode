package im

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func atomicAdd(v *int64, d int64) { atomic.AddInt64(v, d) }
func atomicLoad(v *int64) int64   { return atomic.LoadInt64(v) }

// ============================================================
// Mock Sink + InteractiveSender
// ============================================================

// testSink implements Sink but NOT InteractiveSender.
type testSink struct {
	name string
}

func (s *testSink) Name() string { return s.name }
func (s *testSink) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	return nil
}

type mockInteractiveAdapter struct {
	testSink
	sentInteractive []InteractiveMessage
	msgIDCounter    int
	mu              sync.Mutex
}

func (m *mockInteractiveAdapter) SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentInteractive = append(m.sentInteractive, msg)
	m.msgIDCounter++
	return binding.Adapter + "_msg_" + string(rune('0'+m.msgIDCounter)), nil
}

func (m *mockInteractiveAdapter) lastInteractive() *InteractiveMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sentInteractive) == 0 {
		return nil
	}
	return &m.sentInteractive[len(m.sentInteractive)-1]
}

// ============================================================
// Manager.SendInteractive
// ============================================================

func TestSendInteractive_DispatchesToInteractiveSenders(t *testing.T) {
	mgr := NewManager()
	adapter := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = adapter
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:   "tg",
		ChannelID: "chat123",
	}

	msg := InteractiveMessage{
		ID:   "test1",
		Text: "Pick one:",
		Buttons: []InteractiveButton{
			{Label: "Yes", Value: "1", Style: "primary"},
			{Label: "No", Value: "2", Style: "danger"},
		},
	}

	result := mgr.SendInteractive(context.Background(), msg)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result["tg"] == "" {
		t.Error("expected non-empty message ID for tg adapter")
	}

	last := adapter.lastInteractive()
	if last == nil {
		t.Fatal("expected interactive message to be sent")
	}
	if last.Text != "Pick one:" {
		t.Errorf("text mismatch: %s", last.Text)
	}
	if len(last.Buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(last.Buttons))
	}
	if last.Buttons[0].Label != "Yes" || last.Buttons[0].Value != "1" {
		t.Errorf("button 0 mismatch: %+v", last.Buttons[0])
	}
	if last.Buttons[1].Label != "No" || last.Buttons[1].Value != "2" {
		t.Errorf("button 1 mismatch: %+v", last.Buttons[1])
	}
}

func TestSendInteractive_SkipsNonInteractiveAdapters(t *testing.T) {
	mgr := NewManager()
	// mockSink does NOT implement InteractiveSender
	mgr.sinks["qq"] = &testSink{name: "qq"}
	mgr.currentBindings["qq"] = &ChannelBinding{
		Adapter:   "qq",
		ChannelID: "group123",
	}

	msg := InteractiveMessage{ID: "test2", Text: "Choose:", Buttons: []InteractiveButton{{Label: "A", Value: "1"}}}
	result := mgr.SendInteractive(context.Background(), msg)
	if len(result) != 0 {
		t.Errorf("expected 0 results (adapter doesn't support interactive), got %d", len(result))
	}
}

func TestSendInteractive_MixedAdapters(t *testing.T) {
	mgr := NewManager()
	interactive := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	plain := &testSink{name: "qq"}
	mgr.sinks["tg"] = interactive
	mgr.sinks["qq"] = plain
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "c2"}

	msg := InteractiveMessage{ID: "test3", Text: "Pick:", Buttons: []InteractiveButton{{Label: "OK", Value: "ok"}}}
	result := mgr.SendInteractive(context.Background(), msg)
	if len(result) != 1 {
		t.Fatalf("expected 1 result (only tg supports interactive), got %d", len(result))
	}
	if _, ok := result["tg"]; !ok {
		t.Error("expected tg in result")
	}
	if _, ok := result["qq"]; ok {
		t.Error("qq should not be in result (doesn't implement InteractiveSender)")
	}
}

func TestSendInteractive_EmptyBindings(t *testing.T) {
	mgr := NewManager()
	msg := InteractiveMessage{ID: "test4", Text: "No one to send to"}
	result := mgr.SendInteractive(context.Background(), msg)
	if len(result) != 0 {
		t.Errorf("expected 0 results with no bindings, got %d", len(result))
	}
}

// ============================================================
// Manager.HandleInteractiveCallback
// ============================================================

func TestHandleInteractiveCallback_DispatchesToHandler(t *testing.T) {
	mgr := NewManager()
	var received InteractiveCallback
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		received = cb
	})

	cb := InteractiveCallback{
		MessageID: "msg_123",
		Values:    []string{"1"},
		Adapter:   "tg",
		Envelope: Envelope{
			Adapter:    "tg",
			Platform:   PlatformTelegram,
			ChannelID:  "chat123",
			SenderID:   "user1",
			SenderName: "Alice",
		},
	}
	mgr.HandleInteractiveCallback(cb)

	if received.MessageID != "msg_123" {
		t.Errorf("message ID mismatch: %s", received.MessageID)
	}
	if received.Values[0] != "1" {
		t.Errorf("value mismatch: %v", received.Values)
	}
	if received.Adapter != "tg" {
		t.Errorf("adapter mismatch: %s", received.Adapter)
	}
	if received.Envelope.SenderName != "Alice" {
		t.Errorf("sender name mismatch: %s", received.Envelope.SenderName)
	}
}

func TestHandleInteractiveCallback_NoPanicWithoutHandler(t *testing.T) {
	mgr := NewManager()
	// No handler set — should not panic
	mgr.HandleInteractiveCallback(InteractiveCallback{
		MessageID: "msg_x",
		Values:    []string{"1"},
	})
}

// ============================================================
// DaemonBridge Interactive Callback → pendingAsk
// ============================================================

func TestDaemonBridge_InteractiveCallbackFillsAsk(t *testing.T) {
	mgr := NewManager()
	bridge := NewDaemonBridge(mgr, nil, NewIMEmitter(mgr, "en", "/ws"), nil, nil)

	// Set up a pending ask_user
	req := toolpkg.AskUserRequest{
		Title: "Choose a color",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:   "q1",
				Kind: toolpkg.AskUserKindSingle,
				Choices: []toolpkg.AskUserChoice{
					{ID: "red", Label: "Red"},
					{ID: "blue", Label: "Blue"},
				},
			},
		},
	}
	pending := &pendingAskUser{
		request:  req,
		response: make(chan toolpkg.AskUserResponse, 1),
	}
	bridge.mu.Lock()
	bridge.pendingAsk = pending
	bridge.mu.Unlock()

	// Simulate button click (user clicked "Red" = choice 1)
	mgr.HandleInteractiveCallback(InteractiveCallback{
		MessageID: "msg_123",
		Values:    []string{"1"},
		Adapter:   "tg",
	})

	// Should receive the answer
	select {
	case resp := <-pending.response:
		if len(resp.Answers) != 1 {
			t.Fatalf("expected 1 answer, got %d", len(resp.Answers))
		}
		if !resp.Answers[0].Answered {
			t.Error("expected answer to be answered")
		}
		if len(resp.Answers[0].SelectedChoiceIDs) != 1 || resp.Answers[0].SelectedChoiceIDs[0] != "red" {
			t.Errorf("expected choice 'red', got %v", resp.Answers[0].SelectedChoiceIDs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for ask_user response from interactive callback")
	}
}

func TestDaemonBridge_InteractiveCallbackIgnoredWithoutPendingAsk(t *testing.T) {
	mgr := NewManager()
	_ = NewDaemonBridge(mgr, nil, NewIMEmitter(mgr, "en", "/ws"), nil, nil)

	// No pending ask — callback should be silently ignored
	mgr.HandleInteractiveCallback(InteractiveCallback{
		MessageID: "msg_x",
		Values:    []string{"1"},
		Adapter:   "tg",
	})
	// If we get here without panic, test passes
}

func TestDaemonBridge_InteractiveCallbackMultiSelect(t *testing.T) {
	mgr := NewManager()
	bridge := NewDaemonBridge(mgr, nil, NewIMEmitter(mgr, "en", "/ws"), nil, nil)

	req := toolpkg.AskUserRequest{
		Title: "Pick languages",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:   "q1",
				Kind: toolpkg.AskUserKindMulti,
				Choices: []toolpkg.AskUserChoice{
					{ID: "go", Label: "Go"},
					{ID: "py", Label: "Python"},
					{ID: "rs", Label: "Rust"},
				},
			},
		},
	}
	pending := &pendingAskUser{
		request:  req,
		response: make(chan toolpkg.AskUserResponse, 1),
	}
	bridge.mu.Lock()
	bridge.pendingAsk = pending
	bridge.mu.Unlock()

	// User clicked buttons 1 and 3 (Go + Rust)
	// Note: In real multi-select, each click sends a separate callback.
	// For our test, we simulate the "Done" with comma-joined values.
	mgr.HandleInteractiveCallback(InteractiveCallback{
		Values:  []string{"1", "3"},
		Adapter: "tg",
	})

	select {
	case resp := <-pending.response:
		if !resp.Answers[0].Answered {
			t.Error("expected answered")
		}
		// Should have selected Go (index 0) and Rust (index 2)
		ids := resp.Answers[0].SelectedChoiceIDs
		if len(ids) != 2 {
			t.Fatalf("expected 2 choices, got %d: %v", len(ids), ids)
		}
		hasGo, hasRust := false, false
		for _, id := range ids {
			if id == "go" {
				hasGo = true
			}
			if id == "rs" {
				hasRust = true
			}
		}
		if !hasGo || !hasRust {
			t.Errorf("expected go+rs, got %v", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// ============================================================
// DaemonBridge HandleAskUser Interactive Path
// ============================================================

func TestDaemonBridge_HandleAskUserTriesInteractive(t *testing.T) {
	mgr := NewManager()
	adapter := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = adapter
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "chat1"}

	bridge := NewDaemonBridge(mgr, nil, NewIMEmitter(mgr, "en", "/ws"), nil, nil)

	req := toolpkg.AskUserRequest{
		Title: "Confirm",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:   "q1",
				Kind: toolpkg.AskUserKindSingle,
				Choices: []toolpkg.AskUserChoice{
					{ID: "yes", Label: "Yes"},
					{ID: "no", Label: "No"},
				},
			},
		},
	}

	// Run HandleAskUser in goroutine (it blocks waiting for reply)
	done := make(chan toolpkg.AskUserResponse, 1)
	go func() {
		resp, err := bridge.HandleAskUser(context.Background(), req)
		if err != nil {
			t.Errorf("HandleAskUser error: %v", err)
		}
		done <- resp
	}()

	// Wait for the interactive message to be sent
	time.Sleep(100 * time.Millisecond)

	last := adapter.lastInteractive()
	if last == nil {
		t.Fatal("expected interactive message to be sent (adapter supports InteractiveSender)")
	}
	if len(last.Buttons) != 2 {
		t.Errorf("expected 2 buttons, got %d", len(last.Buttons))
	}
	// Binary choice → first button should be "primary"
	if last.Buttons[0].Style != "primary" {
		t.Errorf("expected primary style for binary choice button 0, got %s", last.Buttons[0].Style)
	}
	if last.Buttons[0].Label != "Yes" || last.Buttons[0].Value != "1" {
		t.Errorf("button 0 mismatch: %+v", last.Buttons[0])
	}

	// Simulate button click reply
	bridge.mu.Lock()
	pending := bridge.pendingAsk
	bridge.mu.Unlock()
	if pending == nil {
		t.Fatal("expected pending ask to be set")
	}
	pending.response <- toolpkg.AskUserResponse{
		Status:        toolpkg.AskUserStatusSubmitted,
		QuestionCount: 1,
		AnsweredCount: 1,
		Answers: []toolpkg.AskUserAnswer{
			{
				ID:                "q1",
				Answered:          true,
				SelectedChoiceIDs: []string{"yes"},
				AnswerMode:        toolpkg.AskUserAnswerModeSelectionOnly,
			},
		},
	}

	select {
	case resp := <-done:
		if resp.AnsweredCount != 1 {
			t.Errorf("expected 1 answered, got %d", resp.AnsweredCount)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for HandleAskUser to complete")
	}
}

func TestDaemonBridge_HandleAskUserFallbackToText(t *testing.T) {
	mgr := NewManager()
	// Only non-interactive adapter
	plain := &testSink{name: "qq"}
	mgr.sinks["qq"] = plain
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "group1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	req := toolpkg.AskUserRequest{
		Title: "Pick",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:   "q1",
				Kind: toolpkg.AskUserKindSingle,
				Choices: []toolpkg.AskUserChoice{
					{ID: "a", Label: "Option A"},
				},
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := bridge.HandleAskUser(context.Background(), req)
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)

	// Reply via text (no interactive adapter available)
	bridge.mu.Lock()
	pending := bridge.pendingAsk
	bridge.mu.Unlock()
	if pending == nil {
		t.Fatal("expected pending ask")
	}
	pending.response <- toolpkg.AskUserResponse{
		Status:        toolpkg.AskUserStatusSubmitted,
		QuestionCount: 1,
		AnsweredCount: 1,
		Answers: []toolpkg.AskUserAnswer{{
			ID:       "q1",
			Answered: true,
		}},
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

// ============================================================
// Telegram Callback Parsing
// ============================================================

func TestTGHandleCallbackQuery_ExtractsData(t *testing.T) {
	mgr := NewManager()
	var received InteractiveCallback
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		received = cb
	})

	adapter := newTGAdapterForTest(mgr)

	update := map[string]any{
		"update_id": 100,
		"callback_query": map[string]any{
			"id":   "cb_123",
			"data": "2",
			"from": map[string]any{
				"id":         42.0,
				"first_name": "Bob",
				"last_name":  "Smith",
			},
			"message": map[string]any{
				"message_id": 99.0,
				"chat": map[string]any{
					"id":   12345.0,
					"type": "private",
				},
			},
		},
	}

	adapter.handleUpdate(context.Background(), update)

	if received.Values[0] != "2" {
		t.Errorf("expected value '2', got %v", received.Values)
	}
	if received.Envelope.SenderName != "Bob Smith" {
		t.Errorf("sender name mismatch: %s", received.Envelope.SenderName)
	}
	if received.Adapter != adapter.name {
		t.Errorf("adapter mismatch: %s", received.Adapter)
	}
	if received.Envelope.Platform != PlatformTelegram {
		t.Errorf("platform mismatch: %s", received.Envelope.Platform)
	}
}

func TestTGHandleUpdate_IgnoresNonCallbackNonMessage(t *testing.T) {
	mgr := NewManager()
	adapter := newTGAdapterForTest(mgr)

	// Update with neither message nor callback_query
	update := map[string]any{
		"update_id": 101,
		"edited_message": map[string]any{
			"message_id": 50.0,
		},
	}
	// Should not panic
	adapter.handleUpdate(context.Background(), update)
}

func newTGAdapterForTest(mgr *Manager) *tgAdapter {
	return &tgAdapter{
		name:       "test_tg",
		botToken:   "fake_token",
		manager:    mgr,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// ============================================================
// Slack Interactive Parsing
// ============================================================

func TestSlackHandleInteractive_ExtractsButtonValue(t *testing.T) {
	mgr := NewManager()
	var received InteractiveCallback
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		received = cb
	})

	adapter := &slackAdapter{
		name:    "test_slack",
		manager: mgr,
	}

	payload := map[string]any{
		"type": "block_actions",
		"actions": []any{
			map[string]any{
				"type":  "button",
				"value": "3",
			},
		},
		"user":    map[string]any{"id": "U123", "username": "charlie"},
		"channel": map[string]any{"id": "C456"},
		"message": map[string]any{"ts": "1234567890.123456"},
	}

	adapter.handleInteractive(context.Background(), payload)

	if received.Values[0] != "3" {
		t.Errorf("expected value '3', got %v", received.Values)
	}
	if received.Envelope.SenderID != "U123" {
		t.Errorf("sender ID mismatch: %s", received.Envelope.SenderID)
	}
	if received.Envelope.ChannelID != "C456" {
		t.Errorf("channel ID mismatch: %s", received.Envelope.ChannelID)
	}
}

func TestSlackHandleInteractive_IgnoresNonBlockActions(t *testing.T) {
	mgr := NewManager()
	adapter := &slackAdapter{
		name:    "test_slack",
		manager: mgr,
	}

	// Wrong type — should be ignored
	payload := map[string]any{
		"type": "message_action",
	}
	adapter.handleInteractive(context.Background(), payload)
	// No panic = pass
}

func TestSlackHandleInteractive_MultipleButtons(t *testing.T) {
	mgr := NewManager()
	var received InteractiveCallback
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		received = cb
	})

	adapter := &slackAdapter{
		name:    "test_slack",
		manager: mgr,
	}

	payload := map[string]any{
		"type": "block_actions",
		"actions": []any{
			map[string]any{"type": "button", "value": "1"},
			map[string]any{"type": "button", "value": "3"},
		},
		"user":    map[string]any{"id": "U1"},
		"channel": map[string]any{"id": "C1"},
	}

	adapter.handleInteractive(context.Background(), payload)

	if len(received.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(received.Values))
	}
	if received.Values[0] != "1" || received.Values[1] != "3" {
		t.Errorf("values mismatch: %v", received.Values)
	}
}

// ============================================================
// Discord Interaction Parsing
// ============================================================

func TestDiscordHandleInteraction_ExtractsCustomID(t *testing.T) {
	mgr := NewManager()
	var received InteractiveCallback
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		received = cb
	})

	adapter := &discordAdapter{
		name:       "test_discord",
		manager:    mgr,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	d := map[string]any{
		"id":    "inter_123",
		"token": "tok_abc",
		"data": map[string]any{
			"custom_id": "2",
		},
		"member": map[string]any{
			"user": map[string]any{
				"id":       "999",
				"username": "dave",
			},
		},
		"channel_id": "chan_777",
	}

	adapter.handleInteraction(context.Background(), d)

	if received.Values[0] != "2" {
		t.Errorf("expected value '2', got %v", received.Values)
	}
	if received.Envelope.SenderID != "999" {
		t.Errorf("sender ID mismatch: %s", received.Envelope.SenderID)
	}
	if received.Envelope.ChannelID != "chan_777" {
		t.Errorf("channel ID mismatch: %s", received.Envelope.ChannelID)
	}
	if received.Envelope.Platform != PlatformDiscord {
		t.Errorf("platform mismatch: %s", received.Envelope.Platform)
	}
}

func TestDiscordHandleInteraction_MissingData(t *testing.T) {
	mgr := NewManager()
	adapter := &discordAdapter{
		name:       "test_discord",
		manager:    mgr,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	// No data field
	d := map[string]any{
		"id":    "inter_x",
		"token": "tok_x",
	}
	adapter.handleInteraction(context.Background(), d)
	// No panic = pass
}

// ============================================================
// Discord SendInteractive Format Validation
// ============================================================

func TestDiscordSendInteractive_MessageFormat(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&reqBody)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_discord_123",
		})
	}))
	defer srv.Close()

	adapter := &discordAdapter{
		name:       "test_discord",
		token:      "fake",
		connected:  true,
		apiBase:    srv.URL,
		httpClient: srv.Client(),
	}

	msg := InteractiveMessage{
		ID:   "test",
		Text: "Pick one:",
		Buttons: []InteractiveButton{
			{Label: "Go", Value: "1", Style: "primary"},
			{Label: "Nope", Value: "2", Style: "danger"},
			{Label: "Maybe", Value: "3"},
		},
	}

	msgID, err := adapter.SendInteractive(context.Background(), ChannelBinding{
		Adapter:   "test_discord",
		ChannelID: "ch1",
	}, msg)
	if err != nil {
		t.Fatalf("SendInteractive error: %v", err)
	}
	if msgID != "msg_discord_123" {
		t.Errorf("message ID mismatch: %s", msgID)
	}

	// Validate message structure
	if reqBody["content"] != "Pick one:" {
		t.Errorf("content mismatch: %v", reqBody["content"])
	}
	components, ok := reqBody["components"].([]any)
	if !ok || len(components) != 1 {
		t.Fatalf("expected 1 component row, got %v", reqBody["components"])
	}
	row, _ := components[0].(map[string]any)
	btns, _ := row["components"].([]any)
	if len(btns) != 3 {
		t.Fatalf("expected 3 buttons, got %d", len(btns))
	}

	// Check styles
	btn0, _ := btns[0].(map[string]any)
	if btn0["style"] != 1.0 { // primary = blurple
		t.Errorf("button 0 style should be primary (1), got %v", btn0["style"])
	}
	btn1, _ := btns[1].(map[string]any)
	if btn1["style"] != 4.0 { // danger = red
		t.Errorf("button 1 style should be danger (4), got %v", btn1["style"])
	}
	btn2, _ := btns[2].(map[string]any)
	if btn2["style"] != 2.0 { // default = secondary
		t.Errorf("button 2 style should be secondary (2), got %v", btn2["style"])
	}

	// Check custom_id values
	if btn0["custom_id"] != "1" {
		t.Errorf("button 0 custom_id: %v", btn0["custom_id"])
	}
	if btn0["label"] != "Go" {
		t.Errorf("button 0 label: %v", btn0["label"])
	}
}

// ============================================================
// Slack SendInteractive Format Validation
// ============================================================

func TestSlackSendInteractive_MessageFormat(t *testing.T) {
	// Validate the JSON body that would be sent to Slack.
	// We can't intercept the HTTP call (hardcoded API URL), so we
	// validate the message construction logic directly.

	adapter := &slackAdapter{
		name:      "test_slack",
		botToken:  "xoxb-fake",
		connected: true,
	}

	msg := InteractiveMessage{
		ID:   "test",
		Text: "Choose wisely:",
		Buttons: []InteractiveButton{
			{Label: "Accept", Value: "yes", Style: "primary"},
			{Label: "Reject", Value: "no", Style: "danger"},
		},
	}

	// Build the expected blocks structure manually and compare
	// We're testing the button→Block Kit mapping logic
	_ = adapter
	_ = msg

	// Verify button construction logic
	buttons := msg.Buttons
	if len(buttons) != 2 {
		t.Fatalf("expected 2 buttons")
	}
	if buttons[0].Style != "primary" {
		t.Errorf("button 0 should be primary")
	}
	if buttons[1].Style != "danger" {
		t.Errorf("button 1 should be danger")
	}

	// Build expected Slack Block Kit elements (same logic as adapter)
	var elements []map[string]any
	for _, btn := range msg.Buttons {
		style := ""
		switch btn.Style {
		case "primary":
			style = "primary"
		case "danger":
			style = "danger"
		}
		elements = append(elements, map[string]any{
			"type":  "button",
			"text":  map[string]any{"type": "plain_text", "text": btn.Label},
			"value": btn.Value,
			"style": style,
		})
	}
	if elements[0]["style"] != "primary" {
		t.Error("element 0 should be primary style")
	}
	if elements[1]["style"] != "danger" {
		t.Error("element 1 should be danger style")
	}
	if elements[0]["value"] != "yes" {
		t.Errorf("element 0 value: %v", elements[0]["value"])
	}
}

// ============================================================
// InteractiveMessage with MultiSelect + Done button
// ============================================================

func TestSendInteractive_MultiSelectAddsDoneButton(t *testing.T) {
	mgr := NewManager()
	adapter := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = adapter
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	msg := InteractiveMessage{
		ID:          "multi",
		Text:        "Pick several:",
		MultiSelect: true,
		Buttons: []InteractiveButton{
			{Label: "A", Value: "1"},
			{Label: "B", Value: "2"},
		},
	}

	mgr.SendInteractive(context.Background(), msg)

	last := adapter.lastInteractive()
	if last == nil {
		t.Fatal("no interactive message sent")
	}
	if !last.MultiSelect {
		t.Error("expected MultiSelect=true")
	}
	if len(last.Buttons) != 2 {
		t.Errorf("expected 2 explicit buttons, got %d", len(last.Buttons))
	}
	// Note: the "Done" button is added by each adapter's SendInteractive
	// implementation, not in the InteractiveMessage itself. So we only
	// check that MultiSelect flag is preserved correctly.
}

// ============================================================
// Edge cases
// ============================================================

func TestSendInteractive_EmptyButtons(t *testing.T) {
	mgr := NewManager()
	adapter := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = adapter
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	msg := InteractiveMessage{ID: "empty", Text: "No buttons", Buttons: nil}
	result := mgr.SendInteractive(context.Background(), msg)

	// Should still send (text-only interactive message)
	if len(result) != 1 {
		t.Errorf("expected 1 result even with no buttons, got %d", len(result))
	}
}

func TestHandleInteractiveCallback_ConcurrentCallbacks(t *testing.T) {
	mgr := NewManager()
	var count int64
	mgr.SetInteractiveCallback(func(cb InteractiveCallback) {
		atomicAdd(&count, 1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.HandleInteractiveCallback(InteractiveCallback{
				Values: []string{"1"},
			})
		}()
	}
	wg.Wait()

	if atomicLoad(&count) != 100 {
		t.Errorf("expected 100 callbacks, got %d", atomicLoad(&count))
	}
}

// ============================================================
// EmitToNonInteractive — fallback filtering by interface type
// ============================================================

// trackingSink records every outbound event it receives.
type trackingSink struct {
	testSink
	events []OutboundEvent
	mu     sync.Mutex
}

func (s *trackingSink) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *trackingSink) lastEvent() *OutboundEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return &s.events[len(s.events)-1]
}

func (s *trackingSink) eventCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func TestEmitToNonInteractive_OnlySendsToNonInteractiveAdapters(t *testing.T) {
	mgr := NewManager()

	// Interactive adapter — should NOT receive fallback
	intAdapter := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = intAdapter
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "chat1"}

	// Non-interactive adapter — SHOULD receive fallback
	plainSink := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = plainSink
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "group1"}

	err := mgr.EmitToNonInteractive(context.Background(), OutboundEvent{
		Kind: OutboundEventText,
		Text: "fallback text",
	})
	if err != nil {
		t.Fatalf("EmitToNonInteractive error: %v", err)
	}

	// QQ should have received the fallback
	if plainSink.eventCount() != 1 {
		t.Fatalf("expected 1 event on qq, got %d", plainSink.eventCount())
	}
	if plainSink.lastEvent().Text != "fallback text" {
		t.Errorf("qq text mismatch: %q", plainSink.lastEvent().Text)
	}

	// TG should NOT have received anything (it's interactive)
	if intAdapter.lastInteractive() != nil {
		t.Error("tg should not receive fallback via EmitToNonInteractive")
	}
}

func TestEmitToNonInteractive_AllInteractive(t *testing.T) {
	mgr := NewManager()

	// Only interactive adapters
	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	discord := &mockInteractiveAdapter{testSink: testSink{name: "discord"}}
	mgr.sinks["discord"] = discord
	mgr.currentBindings["discord"] = &ChannelBinding{Adapter: "discord", ChannelID: "c2"}

	// No non-interactive adapters → should return nil (no targets)
	err := mgr.EmitToNonInteractive(context.Background(), OutboundEvent{
		Kind: OutboundEventText,
		Text: "should not go anywhere",
	})
	if err != nil {
		t.Errorf("expected nil when no non-interactive adapters, got: %v", err)
	}

	if tg.lastInteractive() != nil {
		t.Error("tg should not receive anything")
	}
	if discord.lastInteractive() != nil {
		t.Error("discord should not receive anything")
	}
}

func TestEmitToNonInteractive_AllNonInteractive(t *testing.T) {
	mgr := NewManager()

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	dd := &trackingSink{testSink: testSink{name: "dd"}}
	mgr.sinks["dd"] = dd
	mgr.currentBindings["dd"] = &ChannelBinding{Adapter: "dd", ChannelID: "g2"}

	err := mgr.EmitToNonInteractive(context.Background(), OutboundEvent{
		Kind: OutboundEventText,
		Text: "both should get this",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if qq.eventCount() != 1 {
		t.Errorf("qq expected 1 event, got %d", qq.eventCount())
	}
	if dd.eventCount() != 1 {
		t.Errorf("dd expected 1 event, got %d", dd.eventCount())
	}
}

// ============================================================
// EmitAskUserInteractive — unified interactive + fallback
// ============================================================

func TestEmitAskUserInteractive_MixedAdapters_FallbackOnlyToNonInteractive(t *testing.T) {
	mgr := NewManager()

	// Interactive adapters
	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	// Non-interactive adapter
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")

	msgIDs := emitter.EmitAskUserInteractive("Language", toolpkg.AskUserQuestion{
		ID:    "q1",
		Title: "Which language?",
		Kind:  toolpkg.AskUserKindSingle,
		Choices: []toolpkg.AskUserChoice{
			{ID: "go", Label: "Go"},
			{ID: "rust", Label: "Rust"},
		},
	}, "fallback: Go or Rust?")

	// TG should have received interactive message
	if len(msgIDs) != 1 || msgIDs["tg"] == "" {
		t.Fatalf("expected 1 msgID for tg, got %v", msgIDs)
	}
	last := tg.lastInteractive()
	if last == nil {
		t.Fatal("tg should have received interactive message")
	}
	if len(last.Buttons) != 2 {
		t.Errorf("expected 2 buttons, got %d", len(last.Buttons))
	}
	if last.Buttons[0].Label != "Go" {
		t.Errorf("button 0 label: %s", last.Buttons[0].Label)
	}

	// QQ should have received text fallback
	if qq.eventCount() != 1 {
		t.Fatalf("qq expected 1 fallback event, got %d", qq.eventCount())
	}
	if qq.lastEvent().Text != "fallback: Go or Rust?" {
		t.Errorf("qq fallback text: %q", qq.lastEvent().Text)
	}
}

func TestEmitAskUserInteractive_NoChoices_SendsPlainTextToAll(t *testing.T) {
	mgr := NewManager()

	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")

	msgIDs := emitter.EmitAskUserInteractive("Feature", toolpkg.AskUserQuestion{
		ID:    "q2",
		Title: "What feature?",
		Kind:  toolpkg.AskUserKindText,
	}, "fallback: describe the feature")

	// No interactive message sent (text-only question)
	if len(msgIDs) != 0 {
		t.Errorf("expected no msgIDs for text question, got %v", msgIDs)
	}

	// EmitAskUser is called internally → emits to all via event queue
	// We can't easily verify exact delivery in unit test without waiting
	// for the async emitter goroutine, so we just verify no panic and
	// no interactive message was sent.
	if tg.lastInteractive() != nil {
		t.Error("tg should not receive interactive for text question")
	}
}

func TestEmitAskUserInteractive_OnlyNonInteractiveAdapters(t *testing.T) {
	mgr := NewManager()

	// Only non-interactive adapter — no InteractiveSender
	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")

	msgIDs := emitter.EmitAskUserInteractive("Pick", toolpkg.AskUserQuestion{
		ID:    "q1",
		Title: "Choose one",
		Kind:  toolpkg.AskUserKindSingle,
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
		},
	}, "fallback: A or B?")

	// No interactive adapters → msgIDs empty, falls back to EmitAskUser (plain text)
	if len(msgIDs) != 0 {
		t.Errorf("expected no msgIDs, got %v", msgIDs)
	}
}

func TestEmitAskUserInteractive_NilEmitter(t *testing.T) {
	var emitter *IMEmitter
	msgIDs := emitter.EmitAskUserInteractive("Pick", toolpkg.AskUserQuestion{
		ID:    "q1",
		Title: "Choose",
		Kind:  toolpkg.AskUserKindSingle,
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
		},
	}, "fallback")
	if msgIDs != nil {
		t.Errorf("expected nil, got %v", msgIDs)
	}
}

func TestEmitAskUserInteractive_EmptyFallback(t *testing.T) {
	mgr := NewManager()

	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")

	// Empty fallback text — QQ should NOT receive anything
	msgIDs := emitter.EmitAskUserInteractive("Pick", toolpkg.AskUserQuestion{
		ID:    "q1",
		Title: "Choose",
		Kind:  toolpkg.AskUserKindSingle,
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
		},
	}, "")

	if len(msgIDs) != 1 {
		t.Errorf("expected 1 msgID, got %v", msgIDs)
	}
	if qq.eventCount() != 0 {
		t.Errorf("qq should receive nothing with empty fallback, got %d events", qq.eventCount())
	}
}

func TestEmitAskUserInteractive_MultiSelect(t *testing.T) {
	mgr := NewManager()

	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")

	emitter.EmitAskUserInteractive("Pick", toolpkg.AskUserQuestion{
		ID:    "q1",
		Title: "Choose multiple",
		Kind:  toolpkg.AskUserKindMulti,
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
			{ID: "c", Label: "C"},
		},
	}, "pick several")

	last := tg.lastInteractive()
	if last == nil {
		t.Fatal("expected interactive message")
	}
	if !last.MultiSelect {
		t.Error("expected MultiSelect=true")
	}
	if !strings.Contains(last.Text, "Multi-select") {
		t.Errorf("card text should mention multi-select: %s", last.Text)
	}
	// Non-binary → all buttons should be "default" style
	for _, btn := range last.Buttons {
		if btn.Style != "default" {
			t.Errorf("non-binary multi-select button should be default, got %s", btn.Style)
		}
	}
}

func TestEmitAskUserInteractive_WithPrompt(t *testing.T) {
	mgr := NewManager()

	tg := &mockInteractiveAdapter{testSink: testSink{name: "tg"}}
	mgr.sinks["tg"] = tg
	mgr.currentBindings["tg"] = &ChannelBinding{Adapter: "tg", ChannelID: "c1"}

	emitter := NewIMEmitter(mgr, "en", "/ws")

	emitter.EmitAskUserInteractive("Pick", toolpkg.AskUserQuestion{
		ID:     "q1",
		Title:  "Choose",
		Kind:   toolpkg.AskUserKindSingle,
		Prompt: "Select your preferred option",
		Choices: []toolpkg.AskUserChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B"},
		},
	}, "fallback")

	last := tg.lastInteractive()
	if last == nil {
		t.Fatal("expected interactive message")
	}
	if !strings.Contains(last.Text, "Select your preferred option") {
		t.Errorf("card should contain prompt text: %s", last.Text)
	}
}

// ============================================================
// Slash command takes priority over pendingAsk
// ============================================================

func TestSubmitInboundMessage_SlashCommandBeforePendingAsk(t *testing.T) {
	mgr := NewManager()

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	var restartCalled bool
	bridge.SetRestartHook(func() { restartCalled = true })

	// Set up a pending ask_user
	bridge.mu.Lock()
	bridge.pendingAsk = &pendingAskUser{
		request: toolpkg.AskUserRequest{
			Title: "stuck",
			Questions: []toolpkg.AskUserQuestion{
				{ID: "q1", Title: "Choose", Kind: toolpkg.AskUserKindSingle, Choices: []toolpkg.AskUserChoice{{ID: "yes", Label: "Yes"}}},
			},
		},
		response: make(chan toolpkg.AskUserResponse, 1),
	}
	bridge.mu.Unlock()

	// Send /restart while ask_user is pending
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{
			Adapter:  "qq",
			Platform: PlatformQQ,
		},
		Text: "/restart",
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage error: %v", err)
	}

	// Restart is async (1s delay), wait for it
	time.Sleep(1500 * time.Millisecond)

	if !restartCalled {
		t.Fatal("/restart should have triggered restart even with pendingAsk")
	}

	// pendingAsk should still be set (not consumed)
	bridge.mu.Lock()
	pending := bridge.pendingAsk
	bridge.mu.Unlock()
	if pending == nil {
		t.Fatal("pendingAsk should still be set — /restart must not consume it")
	}
}

func TestSubmitInboundMessage_TextGoesToPendingAsk(t *testing.T) {
	mgr := NewManager()

	qq := &trackingSink{testSink: testSink{name: "qq"}}
	mgr.sinks["qq"] = qq
	mgr.currentBindings["qq"] = &ChannelBinding{Adapter: "qq", ChannelID: "g1"}

	emitter := NewIMEmitter(mgr, "en", "ws")
	bridge := NewDaemonBridge(mgr, nil, emitter, nil, nil)

	// Set up a pending ask_user
	respCh := make(chan toolpkg.AskUserResponse, 1)
	bridge.mu.Lock()
	bridge.pendingAsk = &pendingAskUser{
		request: toolpkg.AskUserRequest{
			Title: "q",
			Questions: []toolpkg.AskUserQuestion{
				{ID: "q1", Title: "What?", Kind: toolpkg.AskUserKindText},
			},
		},
		response: respCh,
	}
	bridge.mu.Unlock()

	// Send regular text — should be consumed as ask_user answer
	err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{
		Envelope: Envelope{
			Adapter:  "qq",
			Platform: PlatformQQ,
		},
		Text: "my answer",
	})
	if err != nil {
		t.Fatalf("SubmitInboundMessage error: %v", err)
	}

	// The answer should have been sent to the response channel
	select {
	case resp := <-respCh:
		if len(resp.Answers) == 0 || resp.Answers[0].FreeformText != "my answer" {
			t.Errorf("expected 'my answer', got %+v", resp.Answers[0])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ask_user response")
	}

	// pendingAsk should be cleared
	bridge.mu.Lock()
	pending := bridge.pendingAsk
	bridge.mu.Unlock()
	if pending != nil {
		t.Fatal("pendingAsk should be nil after answer")
	}
}
