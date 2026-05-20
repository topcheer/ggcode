package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// ─── Pure helper tests ───

func TestTruncateRunes_Short(t *testing.T) {
	result := truncateRunes("hello", 10, "...")
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncateRunes_Long(t *testing.T) {
	result := truncateRunes("hello world!", 5, "...")
	if result != "hello..." {
		t.Errorf("expected 'hello...', got %q", result)
	}
}

func TestTruncateRunes_Unicode(t *testing.T) {
	input := "你好世界测试"
	result := truncateRunes(input, 3, "...")
	if result != "你好世..." {
		t.Errorf("expected '你好世...', got %q", result)
	}
}

func TestTruncateRunes_Exact(t *testing.T) {
	result := truncateRunes("hello", 5, "...")
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

// ─── parseModeFromString tests ───

func TestParseModeFromString(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"supervised", true},
		{"plan", true},
		{"auto", true},
		{"bypass", true},
		{"autopilot", true},
		{"invalid", false},
		{"", false},
		{"AUTO", true},
		{"Plan", true},
	}
	for _, tc := range tests {
		_, ok := parseModeFromString(tc.input)
		if ok != tc.valid {
			t.Errorf("parseModeFromString(%q): expected valid=%v, got %v", tc.input, tc.valid, ok)
		}
	}
}

// ─── tunnelMessagesToHistory tests ───

func TestTunnelMessagesToHistory_UserMessage(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "hello world"},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello world" {
		t.Errorf("unexpected entry: %+v", history[0])
	}
}

func TestTunnelMessagesToHistory_AssistantWithTool(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "let me check"},
			{Type: "tool_use", ToolID: "t1", ToolName: "read_file", Input: json.RawMessage(`{"path":"/tmp/x"}`)},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(history))
	}
	if history[0].Role != "assistant" {
		t.Errorf("entry 0 role: %q", history[0].Role)
	}
	if history[1].Role != "tool_call" || history[1].ToolName != "read_file" {
		t.Errorf("entry 1: %+v", history[1])
	}
}

func TestTunnelMessagesToHistory_ToolResult(t *testing.T) {
	msgs := []provider.Message{
		{Role: "tool", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", ToolName: "read_file", Output: "file content", IsError: false},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	if history[0].Role != "tool_result" || history[0].ToolName != "read_file" {
		t.Errorf("unexpected entry: %+v", history[0])
	}
	if history[0].Result != "file content" {
		t.Errorf("expected result 'file content', got %q", history[0].Result)
	}
}

func TestTunnelMessagesToHistory_TruncatesToolArgs(t *testing.T) {
	longArgs := strings.Repeat("x", 300)
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "tool", Input: json.RawMessage(longArgs)},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	if len(history[0].ToolArgs) > 203 {
		t.Errorf("tool args not truncated: %d chars", len(history[0].ToolArgs))
	}
}

func TestTunnelMessagesToHistory_TruncatesResult(t *testing.T) {
	longResult := strings.Repeat("y", 600)
	msgs := []provider.Message{
		{Role: "tool", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", ToolName: "tool", Output: longResult},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history[0].Result) > 503 {
		t.Errorf("result not truncated: %d chars", len(history[0].Result))
	}
}

func TestTunnelMessagesToHistory_Empty(t *testing.T) {
	history := tunnelMessagesToHistory(nil)
	if history != nil {
		t.Errorf("expected nil, got %v", history)
	}
}

func TestTunnelMessagesToHistory_SkipsEmpty(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "   "}, // whitespace-only
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: ""}, // empty
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 0 {
		t.Errorf("expected 0 entries for whitespace-only messages, got %d", len(history))
	}
}

// ─── Full conversation history test ───

func TestTunnelMessagesToHistory_FullConversation(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "fix the bug"},
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "I'll fix it."},
			{Type: "tool_use", ToolID: "t1", ToolName: "edit_file", Input: json.RawMessage(`{"path":"/tmp/x"}`)},
		}},
		{Role: "tool", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", ToolName: "edit_file", Output: "OK"},
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: "Done!"},
		}},
	}

	history := tunnelMessagesToHistory(msgs)
	expected := []struct {
		role     string
		content  string
		toolName string
	}{
		{"user", "fix the bug", ""},
		{"assistant", "I'll fix it.", ""},
		{"tool_call", "", "edit_file"},
		{"tool_result", "OK", "edit_file"},
		{"assistant", "Done!", ""},
	}

	if len(history) != len(expected) {
		t.Fatalf("expected %d entries, got %d", len(expected), len(history))
	}
	for i, exp := range expected {
		if history[i].Role != exp.role {
			t.Errorf("entry %d: expected role %q, got %q", i, exp.role, history[i].Role)
		}
		if exp.content != "" && history[i].Content != exp.content && history[i].Result != exp.content {
			// Content or Result depending on role
		}
		if exp.toolName != "" && history[i].ToolName != exp.toolName {
			t.Errorf("entry %d: expected toolName %q, got %q", i, exp.toolName, history[i].ToolName)
		}
	}
}

// ─── Nil broker safety tests ───

func TestPushTunnelEvent_NilBroker(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	// Should not panic on any event type
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "hello"})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "t1", Name: "tool"}})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "t1"}, Result: "ok"})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventError})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventSystem})
}

func TestAllPushMethods_NilBroker(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	// None of these should panic
	m.pushTunnelUserMessage("test")
	m.pushTunnelStatusThinking()
	m.pushTunnelCancel()
	m.pushSubAgentTunnelStreamText("sa-1", "text")
	m.pushSubAgentTunnelToolCall("sa-1", "t1", "tool", "{}", "")
	m.pushSubAgentTunnelToolResult("sa-1", "t1", "tool", "result", false)
	m.pushSubAgentTunnelEvent(&subagent.SubAgent{ID: "x", Status: subagent.StatusRunning})
	m.pushSubAgentTunnelEvent(&subagent.SubAgent{ID: "x", Status: subagent.StatusCompleted, Result: "done"})
	m.pushSubAgentTunnelEvent(&subagent.SubAgent{ID: "x", Status: subagent.StatusFailed})
	m.pushSubAgentTunnelEvent(&subagent.SubAgent{ID: "x", Status: subagent.StatusCancelled})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_spawned", TeammateID: "x"})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_tool_call", TeammateID: "x", ToolID: "t1", CurrentTool: "tool", ToolArgs: `{}`})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_tool_result", TeammateID: "x", ToolID: "t1"})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_text", TeammateID: "x", Result: "text"})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_working", TeammateID: "x", TeammateName: "coder"})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_idle", TeammateID: "x", TeammateName: "coder", Result: "done"})
	m.pushSwarmTunnelEvent(swarm.Event{Type: "teammate_shutdown", TeammateID: "x", TeammateName: "coder"})
}

// ─── handleTunnelClientCommand nil-safety tests ───

func TestHandleTunnelClientCommand_InvalidJSON(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdMessage, Data: []byte("not json")}
	m.handleTunnelClientCommand(cmd) // should not panic
}

func TestHandleTunnelClientCommand_EmptyText(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	data, _ := json.Marshal(tunnel.MessageData{Text: ""})
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdMessage, Data: data}
	m.handleTunnelClientCommand(cmd) // should not panic
}

func TestHandleTunnelClientCommand_Interrupt(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdInterrupt}
	m.handleTunnelClientCommand(cmd) // should not panic
}

func TestHandleTunnelClientCommand_ModeChange(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	data, _ := json.Marshal(tunnel.ModeChangeData{Mode: "auto"})
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdModeChange, Data: data}
	m.handleTunnelClientCommand(cmd) // should not panic
}

// ─── handleTunnelModeChangeMsg tests ───

func TestHandleTunnelModeChangeMsg_ValidMode(t *testing.T) {
	m := newTestModel()
	m.mode = permission.SupervisedMode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(permission.SupervisedMode)
	}

	_, _ = m.handleTunnelModeChangeMsg(tunnelModeChangeMsg{mode: "auto"})

	if m.mode != permission.AutoMode {
		t.Errorf("expected mode=auto, got %v", m.mode)
	}
}

func TestHandleTunnelModeChangeMsg_InvalidMode(t *testing.T) {
	m := newTestModel()
	m.mode = permission.SupervisedMode

	_, _ = m.handleTunnelModeChangeMsg(tunnelModeChangeMsg{mode: "invalid_mode"})

	// Should not change — ParsePermissionMode returns supervised for unknown,
	// and our guard rejects supervised when input wasn't "supervised"
	if m.mode != permission.SupervisedMode {
		t.Errorf("mode should not change for invalid input, got %v", m.mode)
	}
}

// ─── Tunnel command handler nil-safety ───

func TestHandleTunnelCommand_StatusInactive(t *testing.T) {
	m := newTestModel()
	_ = m.handleTunnelCommand("/tunnel status")
}

func TestHandleTunnelCommand_Usage(t *testing.T) {
	m := newTestModel()
	_ = m.handleTunnelCommand("/tunnel xyz")
}

func TestHandleTunnelCommand_StopNoTunnel(t *testing.T) {
	m := newTestModel()
	_ = m.handleTunnelCommand("/tunnel stop")
}

func TestHandleTunnelCommand_ShareAlias(t *testing.T) {
	m := newTestModel()
	_ = m.handleTunnelCommand("/share status")
}

func TestHandleTunnelApprovalResponse_IgnoresMismatchedID(t *testing.T) {
	m := newTestModel()
	m.pendingApproval = &ApprovalMsg{ToolName: "run_command"}
	m.tunnelPendingApprovalID = "req-1"

	got, _ := m.handleTunnelApprovalResponse(tunnelApprovalResponseMsg{id: "req-2", decision: "allow"})
	updated := got.(*Model)
	if updated.pendingApproval == nil {
		t.Fatal("expected mismatched approval response to be ignored")
	}
	if updated.tunnelPendingApprovalID != "req-1" {
		t.Fatalf("expected pending approval id to remain, got %q", updated.tunnelPendingApprovalID)
	}
}

func TestBuildAskUserResponseFromTunnel(t *testing.T) {
	req := toolpkg.AskUserRequest{
		Title: "Clarify",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:            "area",
				Title:         "Area",
				Prompt:        "Which area?",
				Kind:          toolpkg.AskUserKindSingle,
				AllowFreeform: true,
				Choices:       []toolpkg.AskUserChoice{{ID: "frontend", Label: "Frontend"}},
			},
			{
				ID:     "notes",
				Title:  "Notes",
				Prompt: "Anything else?",
				Kind:   toolpkg.AskUserKindText,
			},
		},
	}
	resp := buildAskUserResponseFromTunnel(req, toolpkg.AskUserStatusSubmitted, []tunnel.AskUserAnswer{
		{QuestionID: "area", ChoiceIDs: []string{"frontend"}, FreeformText: "Ship today"},
		{QuestionID: "notes", FreeformText: "Focus on UX"},
	})
	if resp.Status != toolpkg.AskUserStatusSubmitted {
		t.Fatalf("expected submitted status, got %q", resp.Status)
	}
	if resp.AnsweredCount != 2 {
		t.Fatalf("expected answered_count=2, got %d", resp.AnsweredCount)
	}
	if len(resp.Answers) != 2 {
		t.Fatalf("expected 2 answers, got %d", len(resp.Answers))
	}
	if got := resp.Answers[0].SelectedChoices; len(got) != 1 || got[0] != "Frontend" {
		t.Fatalf("expected selected choice label to be preserved, got %+v", got)
	}
	if resp.Answers[0].AnswerMode != toolpkg.AskUserAnswerModeSelectionAndFreeform {
		t.Fatalf("expected selection_and_freeform, got %q", resp.Answers[0].AnswerMode)
	}
	if !resp.Answers[1].Answered {
		t.Fatal("expected freeform text question to count as answered")
	}
}
