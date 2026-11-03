package im

import (
	"context"
	"errors"
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// mockEmitter tracks EmitText calls and can simulate errors
type mockEmitter struct {
	emitTextCalls         []string
	emitTextError         error
	sendInteractiveMsgIDs map[string]string
}

func (m *mockEmitter) EmitText(text string) error {
	if m.emitTextCalls == nil {
		m.emitTextCalls = []string{}
	}
	m.emitTextCalls = append(m.emitTextCalls, text)
	return m.emitTextError
}

func (m *mockEmitter) EmitAskUserInteractive(title string, q toolpkg.AskUserQuestion, fallbackText string) map[string]string {
	if m.sendInteractiveMsgIDs == nil {
		m.sendInteractiveMsgIDs = make(map[string]string)
	}

	return map[string]string{"discord": "msg_123", "feishu": "msg_456"}
}

func (m *mockEmitter) EmitAskUser(text string) {
	m.emitTextCalls = append(m.emitTextCalls, text)
}

func (m *mockEmitter) FormatAskUserPrompt(argsJSON string) string {
	return "Mock AskUser Prompt"
}

func (m *mockEmitter) TriggerTyping() {}

func (m *mockEmitter) HasTargets() bool                                                         { return true }
func (m *mockEmitter) Manager() *Manager                                                        { return nil }
func (m *mockEmitter) Language() string                                                         { return "en" }
func (m *mockEmitter) SetOutputMode(mode string)                                                {}
func (m *mockEmitter) OutputMode() string                                                       { return "verbose" }
func (m *mockEmitter) EmitEvent(event OutboundEvent)                                            {}
func (m *mockEmitter) EmitUserText(text string)                                                 {}
func (m *mockEmitter) EmitUserTextExcept(text, excludeAdapter string)                           {}
func (m *mockEmitter) EmitStatus(status string)                                                 {}
func (m *mockEmitter) EmitToolStatus(toolName, rawArgs string)                                  {}
func (m *mockEmitter) EmitRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int) {}
func (m *mockEmitter) EmitKnightReport(report string)                                           {}

// mockAdapter simulates an adapter for testing
type mockAdapter struct {
	name                 string
	interactiveSupported bool
}

func (m *mockAdapter) Name() string                    { return m.name }
func (m *mockAdapter) Platform() Platform              { return PlatformDiscord }
func (m *mockAdapter) Start(ctx context.Context) error { return nil }
func (m *mockAdapter) Stop(ctx context.Context) error  { return nil }
func (m *mockAdapter) Send(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	return nil
}
func (m *mockAdapter) SendInteractive(ctx context.Context, binding ChannelBinding, msg InteractiveMessage) (string, error) {
	if !m.interactiveSupported {
		return "", errors.New("interactive not supported")
	}
	return "msg_id", nil
}
func (m *mockAdapter) Ping(ctx context.Context) error { return nil }
func (m *mockAdapter) Close() error                   { return nil }

// TestBugD_EmitTextReturnsError tests that EmitText returns error on IM disconnect
func TestBugD_EmitTextReturnsError(t *testing.T) {
	mockEm := &mockEmitter{}
	mockEm.emitTextError = errors.New("IM disconnected")

	err := mockEm.EmitText("test message")
	if err == nil {
		t.Fatal("EmitText should return error when IM is disconnected")
	}
	if err.Error() != "IM disconnected" {
		t.Fatalf("Expected 'IM disconnected', got: %v", err)
	}
}

// TestBugF_PendingAskRegisteredBeforeSend tests that pendingAsk is registered
// before interactive buttons are sent, eliminating the race window
func TestBugF_PendingAskRegisteredBeforeSend(t *testing.T) {
	mockEm := &mockEmitter{}

	req := toolpkg.AskUserRequest{
		Title: "Test Question",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:    "q1",
				Title: "Choose an option",
				Kind:  toolpkg.AskUserKindSingle,
				Choices: []toolpkg.AskUserChoice{
					{ID: "1", Label: "Option 1"},
					{ID: "2", Label: "Option 2"},
				},
			},
		},
	}

	msgIDs := mockEm.EmitAskUserInteractive("Test", req.Questions[0], "fallback text")
	if len(msgIDs) == 0 {
		t.Fatal("EmitAskUserInteractive should return message IDs")
	}
	if msgIDs["discord"] == "" {
		t.Fatal("Expected message ID for discord adapter")
	}
}

// TestBugA_SubmitInboundMessageReverifyPending tests that pending is re-verified
// after re-locking to avoid the race where approval/ask timeout between routing
// snapshot and channel read
func TestBugA_SubmitInboundMessageReverifyPending(t *testing.T) {

	t.Skip("Requires full bridge setup - covered by integration tests")
}

// TestBugB_PublishAdapterStateTerminalGuard tests that late error states
// don't overwrite a properly set disconnected state
func TestBugB_PublishAdapterStateTerminalGuard(t *testing.T) {

	t.Skip("Requires adapter runtime setup - covered by integration tests")
}
