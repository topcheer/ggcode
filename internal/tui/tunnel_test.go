package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
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
	if history[1].ToolDisplayName != "Read" {
		t.Errorf("entry 1 display name: %q", history[1].ToolDisplayName)
	}
}

func TestTunnelMessagesToHistory_AssistantReasoning(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{ReasoningContent: "checking options"},
			{Type: "text", Text: "done"},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(history))
	}
	if history[0].Role != "reasoning" || history[0].Content != "checking options" {
		t.Fatalf("unexpected reasoning entry: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "done" {
		t.Fatalf("unexpected assistant entry: %+v", history[1])
	}
}

func TestTunnelMessagesToHistory_ToolResult(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
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

func TestTunnelMessagesToHistory_TruncatesToolArgsWithoutBreakingUTF8(t *testing.T) {
	rawArgs := `"` + strings.Repeat("a", 198) + `你"`
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "tool", Input: json.RawMessage(rawArgs)},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	if !utf8.ValidString(history[0].ToolArgs) {
		t.Fatalf("expected valid UTF-8 tool args, got %q", history[0].ToolArgs)
	}
}

func TestTunnelMessagesToHistoryStoresToolDetail(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "run_command", Input: json.RawMessage(`{"command":"cd /tmp && go test ./..."}`)},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	// describeTool returns localized label for run_command (no comment line in command)
	if history[0].ToolDetail == "" {
		t.Fatal("expected tool_detail to be populated for tool history")
	}
	if history[0].ToolDisplayName != "Run" {
		t.Fatalf("expected command display name, got %q", history[0].ToolDisplayName)
	}
}

func TestTunnelMessagesToHistoryStoresToolDisplayNameFromDescription(t *testing.T) {
	msgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "run_command", Input: json.RawMessage(`{"description":"run tests","command":"cd /tmp && go test ./..."}`)},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	// describeTool uses description field: desc + " (" + friendlyToolName + ")"
	if history[0].ToolDisplayName != "run tests (Bash)" {
		t.Fatalf("expected tool display name from description, got %q", history[0].ToolDisplayName)
	}
}

func TestTunnelMessagesToHistory_TruncatesResult(t *testing.T) {
	longResult := strings.Repeat("y", 600)
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", ToolName: "tool", Output: longResult},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history[0].Result) > 503 {
		t.Errorf("result not truncated: %d chars", len(history[0].Result))
	}
}

func TestTunnelMessagesToHistory_TruncatesResultWithoutBreakingUTF8(t *testing.T) {
	longResult := strings.Repeat("y", 499) + "你"
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", ToolName: "tool", Output: longResult},
		}},
	}
	history := tunnelMessagesToHistory(msgs)
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
	if !utf8.ValidString(history[0].Result) {
		t.Fatalf("expected valid UTF-8 result, got %q", history[0].Result)
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
		{Role: "user", Content: []provider.ContentBlock{
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

func TestCurrentSessionMessagesFallsBackToSessionStoreMessages(t *testing.T) {
	m := newTestModel()
	m.agent = nil
	m.session = &session.Session{
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("hello")}},
		},
	}

	msgs := m.currentSessionMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected session-backed messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("expected restored user message, got %q", msgs[0].Role)
	}
}

func TestCurrentTunnelHistoryPreservesSystemAndToolBoundaries(t *testing.T) {
	m := newTestModel()
	m.chatWriteUser("user-1", "check mobile release")
	m.chatWriteSystem("sys-1", "rerun is still running")

	before := chat.NewAssistantItem("assistant-1", m.chatStyles)
	before.SetText("I checked the current run.")
	before.SetFinished()
	m.chatList.Append(before)

	toolItem := chat.NewToolItem("tool-1", chat.ToolContext{
		ToolName:    "run_command",
		DisplayName: "Check status",
		Detail:      "gh run list --limit 3",
		RawArgs:     `{"command":"gh run list --limit 3"}`,
		Lang:        "en",
	}, chat.StatusSuccess, m.chatStyles)
	if setter, ok := toolItem.(interface{ SetResult(string, bool) }); ok {
		setter.SetResult("completed success release", false)
	}
	m.chatList.Append(toolItem)

	after := chat.NewAssistantItem("assistant-2", m.chatStyles)
	after.SetText("The rerun completed successfully.")
	after.SetFinished()
	m.chatList.Append(after)

	history := m.currentTunnelHistory()
	if len(history) != 6 {
		t.Fatalf("expected 6 history entries, got %d: %+v", len(history), history)
	}
	if history[0].Role != "user" || history[0].Content != "check mobile release" {
		t.Fatalf("unexpected first history entry: %+v", history[0])
	}
	if history[1].Role != "system" || history[1].Content != "rerun is still running" {
		t.Fatalf("unexpected system history entry: %+v", history[1])
	}
	if history[2].Role != "assistant" || history[2].Content != "I checked the current run." {
		t.Fatalf("unexpected assistant-before entry: %+v", history[2])
	}
	if history[3].Role != "tool_call" || history[4].Role != "tool_result" {
		t.Fatalf("expected tool call/result entries, got %+v", history[3:])
	}
	if history[5].Role != "assistant" || history[5].Content != "The rerun completed successfully." {
		t.Fatalf("unexpected assistant-after entry: %+v", history[5])
	}
}

func TestCurrentTunnelHistoryPreservesLocalizedToolDescription(t *testing.T) {
	m := newTestModel()
	rawArgs := `{"description":"编译","command":"go build ./cmd/ggcode"}`
	toolItem := chat.NewToolItem("tool-1", chat.ToolContext{
		ToolName:    "run_command",
		DisplayName: "编译 (Bash)",
		Detail:      "go build ./cmd/ggcode",
		RawArgs:     rawArgs,
		Lang:        "zh-CN",
	}, chat.StatusSuccess, m.chatStyles)
	m.chatList.Append(toolItem)

	history := m.currentTunnelHistory()
	if len(history) != 2 {
		t.Fatalf("expected tool_call and tool_result, got %+v", history)
	}
	if history[0].Role != "tool_call" {
		t.Fatalf("unexpected first history entry: %+v", history[0])
	}
	if history[0].ToolName != "run_command" {
		t.Fatalf("tool_call name = %q, want run_command", history[0].ToolName)
	}
	if history[0].ToolDisplayName != "编译 (Bash)" {
		t.Fatalf("tool_call display_name = %q, want %q", history[0].ToolDisplayName, "编译 (Bash)")
	}
	if strings.ContainsRune(history[0].ToolDisplayName, '\uFFFD') {
		t.Fatalf("tool_call display_name contains replacement char: %q", history[0].ToolDisplayName)
	}
	if history[0].ToolArgs != rawArgs {
		t.Fatalf("tool_call args = %q, want raw args", history[0].ToolArgs)
	}
}

func TestTunnelSnapshotMergesIncompleteLedgerTail(t *testing.T) {
	m := newTestModel()
	store := newTestSessionStore(t)
	ses := &session.Session{
		ID:        "sess-mobile-tail",
		CreatedAt: time.Now().Add(-time.Minute),
		UpdatedAt: time.Now(),
		TunnelEvents: []session.TunnelEvent{
			{
				EventID: "ev-000000001",
				Type:    tunnel.EventUserMessage,
				Data:    json.RawMessage(`{"text":"tui 不用改是么?"}`),
			},
			{
				EventID:  "ev-000000002",
				StreamID: "msg-1",
				Type:     tunnel.EventText,
				Data:     json.RawMessage(`{"id":"msg-1","chunk":"不用改。","done":false}`),
			},
			{
				EventID:  "ev-000000003",
				StreamID: "msg-1",
				Type:     tunnel.EventTextDone,
				Data:     json.RawMessage(`{"id":"msg-1","done":true}`),
			},
		},
	}
	m.SetSession(ses, store)
	m.chatWriteUser("user-1", "tui 不用改是么?")

	snapshot := m.tunnelSnapshot()
	if len(snapshot.History) != 2 {
		t.Fatalf("expected merged snapshot history, got %d entries: %+v", len(snapshot.History), snapshot.History)
	}
	if snapshot.History[0].Role != "user" || snapshot.History[0].Content != "tui 不用改是么?" {
		t.Fatalf("unexpected first history entry: %+v", snapshot.History[0])
	}
	if snapshot.History[1].Role != "assistant" || snapshot.History[1].Content != "不用改。" {
		t.Fatalf("expected tail assistant reply to be preserved, got %+v", snapshot.History[1])
	}
}

func TestPrepareCurrentSessionTunnelLedgerDowngradesPartialReplayLedger(t *testing.T) {
	store := newTestSessionStore(t)
	m := newTestModel()
	m.sessionStore = store

	ses := &session.Session{
		ID:        "sess-replay",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("fix the release job")}},
			{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("I checked the failure and found a version-code conflict.")}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "tool_use", ToolID: "tool-1", ToolName: "run_command", Input: json.RawMessage(`{"command":"gh run list --limit 3"}`)},
			}},
			{Role: "user", Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: "tool-1", ToolName: "run_command", Output: "completed success release"},
			}},
			{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("Done. The rerun succeeded.")}},
		},
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{
				EventID:  "ev-000000010",
				StreamID: "tool-1",
				Type:     tunnel.EventToolCall,
				Data:     json.RawMessage(`{"tool_id":"tool-1","tool_name":"run_command","display_name":"Check Mobile Release rerun status","args":"{\"command\":\"gh run list --limit 3\"}","detail":"gh run list --limit 3"}`),
			},
			{
				EventID:  "ev-000000011",
				StreamID: "tool-1",
				Type:     tunnel.EventToolResult,
				Data:     json.RawMessage(`{"tool_id":"tool-1","tool_name":"run_command","result":"completed success release","is_error":false}`),
			},
		},
	}
	m.SetSession(ses, store)
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}

	m.prepareCurrentSessionTunnelLedger()

	if m.session.TunnelEventsComplete {
		t.Fatal("expected partial replay ledger to be downgraded")
	}
	if len(m.currentSessionTunnelReplayEvents()) != 0 {
		t.Fatal("expected downgraded session to fall back to snapshot replay")
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if loaded.TunnelEventsComplete {
		t.Fatal("expected downgraded replay flag to persist")
	}
}

func TestApplyResumedSessionClearsAgentContext(t *testing.T) {
	m := newTestModel()
	m.agent = agent.NewAgent(nil, toolpkg.NewRegistry(), "", 1)
	m.agent.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{provider.TextBlock("stale context")},
	})

	ses := &session.Session{
		ID: "sess-resume-agent",
		Messages: []provider.Message{
			{
				Role:    "user",
				Content: []provider.ContentBlock{provider.TextBlock("fresh context")},
			},
		},
	}

	m.applyResumedSession(ses)

	msgs := m.agent.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected resumed agent context to contain 1 message, got %d", len(msgs))
	}
	if got := msgs[0].Content[0].Text; got != "fresh context" {
		t.Fatalf("expected resumed agent context to be replaced, got %q", got)
	}
}

func TestApplyResumedSessionPreservesCanonicalReplay(t *testing.T) {
	m := newTunnelRecordingModel(t)
	store := m.sessionStore

	m.chatWriteUser("stale-user", "stale live chat")

	ses := &session.Session{
		ID:        "sess-resume-replay",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("restored user")}},
			{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("restored assistant")}},
		},
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{
				EventID: "ev-000000001",
				Type:    tunnel.EventUserMessage,
				Data:    json.RawMessage(`{"text":"restored user"}`),
			},
			{
				EventID:  "ev-000000002",
				StreamID: "msg-1",
				Type:     tunnel.EventText,
				Data:     json.RawMessage(`{"id":"msg-1","chunk":"restored assistant"}`),
			},
			{
				EventID:  "ev-000000003",
				StreamID: "msg-1",
				Type:     tunnel.EventTextDone,
				Data:     json.RawMessage(`{"id":"msg-1","done":true}`),
			},
		},
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}

	m.applyResumedSession(ses)
	if got := len(m.currentSessionTunnelReplayEvents()); got != len(ses.TunnelEvents) {
		t.Fatalf("expected canonical replay to remain available after resume, got %d events", got)
	}

	m.publishTunnelSnapshotForCurrentSession(true)

	if !m.session.TunnelEventsComplete {
		t.Fatal("expected canonical replay to remain armed after publish")
	}
	if got := len(m.currentSessionTunnelReplayEvents()); got != len(ses.TunnelEvents) {
		t.Fatalf("expected publish to keep canonical replay intact, got %d events", got)
	}
}

func TestPrepareCurrentSessionTunnelLedgerMarksFreshSessionComplete(t *testing.T) {
	store := newTestSessionStore(t)
	m := newTestModel()
	m.sessionStore = store
	ses := &session.Session{
		ID:        "sess-fresh",
		CreatedAt: time.Now().Add(-time.Minute),
		UpdatedAt: time.Now(),
	}
	m.SetSession(ses, store)
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}

	m.prepareCurrentSessionTunnelLedger()

	if !m.session.TunnelEventsComplete {
		t.Fatal("expected fresh tunnel session to arm canonical replay")
	}
}

func TestResetCurrentSessionTunnelLedgerClearsCanonicalReplay(t *testing.T) {
	store := newTestSessionStore(t)
	m := newTestModel()
	m.sessionStore = store
	ses := &session.Session{
		ID:                   "sess-reset",
		CreatedAt:            time.Now().Add(-time.Minute),
		UpdatedAt:            time.Now(),
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{EventID: "ev-000000001", Type: tunnel.EventUserMessage, Data: []byte(`{"text":"hello"}`)},
		},
	}
	m.SetSession(ses, store)
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}

	m.resetCurrentSessionTunnelLedger()

	if len(m.session.TunnelEvents) != 0 {
		t.Fatalf("expected reset ledger to clear tunnel events, got %d", len(m.session.TunnelEvents))
	}
	if m.session.TunnelEventsComplete {
		t.Fatal("expected reset ledger to require fresh canonical replay")
	}
}

func TestBindTunnelProjectionSessionBackfillsSessionInfoIntoProjectionStore(t *testing.T) {
	store := newTestSessionStore(t)
	projectionStore, err := tunnel.NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new projection store: %v", err)
	}
	if err := projectionStore.Append(tunnel.GatewayMessage{
		SessionID: "sess-bootstrap",
		EventID:   "ev-000000001",
		Type:      tunnel.EventSystemMessage,
		Data:      json.RawMessage(`{"text":"hello"}`),
	}); err != nil {
		t.Fatalf("seed projection event: %v", err)
	}

	m := newTestModel()
	m.sessionStore = store
	m.tunnelProjectionStore = projectionStore
	m.activeModel = "gpt-test"
	m.activeVendor = "openai"
	ses := &session.Session{
		ID:        "sess-bootstrap",
		CreatedAt: time.Now().Add(-time.Minute),
		UpdatedAt: time.Now(),
	}

	m.SetSession(ses, store)

	replay, err := projectionStore.ReplayEvents("sess-bootstrap")
	if err != nil {
		t.Fatalf("load projection replay: %v", err)
	}
	if len(replay) < 2 {
		t.Fatalf("expected replay to include bootstrap plus seeded event, got %d", len(replay))
	}
	var sawSystemMessage bool
	var sawSessionInfo bool
	for _, ev := range replay {
		if ev.Type == tunnel.EventSystemMessage {
			sawSystemMessage = true
		}
		if ev.Type == tunnel.EventSessionInfo {
			sawSessionInfo = true
		}
	}
	if !sawSessionInfo {
		t.Fatalf("expected replay to include session_info bootstrap, got %#v", replay)
	}
	if !sawSystemMessage {
		t.Fatalf("expected replay to retain seeded system_message, got %#v", replay)
	}
}

func TestBindTunnelProjectionSessionHydratesIncompleteSessionLedgerIntoProjectionReplay(t *testing.T) {
	store := newTestSessionStore(t)
	projectionStore, err := tunnel.NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new projection store: %v", err)
	}
	if err := projectionStore.Append(tunnel.GatewayMessage{
		SessionID: "sess-old",
		EventID:   "ev-000000001",
		Type:      tunnel.EventSystemMessage,
		Data:      json.RawMessage(`{"text":"shared started"}`),
	}); err != nil {
		t.Fatalf("seed projection event: %v", err)
	}

	m := newTestModel()
	m.sessionStore = store
	m.tunnelProjectionStore = projectionStore
	ses := &session.Session{
		ID:                   "sess-old",
		CreatedAt:            time.Now().Add(-time.Hour),
		UpdatedAt:            time.Now(),
		TunnelEventsComplete: false,
		TunnelEvents: []session.TunnelEvent{
			{
				EventID:  "ev-000000101",
				StreamID: "msg-1-reasoning",
				Type:     tunnel.EventReasoning,
				Data:     json.RawMessage(`{"id":"msg-1-reasoning","chunk":"thinking"}`),
			},
			{
				EventID:  "ev-000000102",
				StreamID: "msg-1",
				Type:     tunnel.EventText,
				Data:     json.RawMessage(`{"id":"msg-1","chunk":"answer"}`),
			},
			{
				EventID:  "ev-000000103",
				StreamID: "msg-1",
				Type:     tunnel.EventTextDone,
				Data:     json.RawMessage(`{"id":"msg-1","done":true}`),
			},
		},
	}

	m.SetSession(ses, store)

	replay := m.currentSessionTunnelReplayEvents()
	if len(replay) < 4 {
		t.Fatalf("expected replay to include system + hydrated tunnel events, got %d", len(replay))
	}
	var sawReasoning, sawText, sawTextDone bool
	for _, ev := range replay {
		switch ev.Type {
		case tunnel.EventReasoning:
			sawReasoning = true
		case tunnel.EventText:
			sawText = true
		case tunnel.EventTextDone:
			sawTextDone = true
		}
	}
	if !sawReasoning || !sawText || !sawTextDone {
		t.Fatalf("expected replay to include hydrated reasoning/text events, got %#v", replay)
	}
}

func TestCurrentSessionTunnelReplayEventsSortsLegacyLedgerByEventID(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel()
	ses := &session.Session{
		ID:                   "sess-old",
		CreatedAt:            time.Now().Add(-time.Hour),
		UpdatedAt:            time.Now(),
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{EventID: "ev-000000003", Type: tunnel.EventTextDone, Data: json.RawMessage(`{"id":"msg-1","done":true}`)},
			{EventID: "ev-000000001", Type: tunnel.EventText, Data: json.RawMessage(`{"id":"msg-1","chunk":"hello"}`)},
			{EventID: "ev-000000002", Type: tunnel.EventActivity, Data: json.RawMessage(`{"activity":"thinking"}`)},
		},
	}

	m.SetSession(ses, store)

	replay := m.currentSessionTunnelReplayEvents()
	got := []string{replay[0].EventID, replay[1].EventID, replay[2].EventID}
	want := []string{"ev-000000001", "ev-000000002", "ev-000000003"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replay[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestTunnelSnapshotMatchesDetectsMidShareProjectionGap(t *testing.T) {
	seeded := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		Status:      tunnel.StatusData{Status: tunnel.StatusBusy},
		Activity:    tunnel.ActivityData{Activity: "Collecting project knowledge..."},
		History: []tunnel.HistoryEntry{
			{Role: "system", Content: "Starting tunnel..."},
			{Role: "tool_call", ToolID: "tool-1", ToolName: "bash", ToolDisplayName: "Run bash", ToolArgs: `{"command":"sleep 1"}`},
		},
	}
	latest := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		Status:      tunnel.StatusData{Status: tunnel.StatusIdle, Message: ""},
		History: []tunnel.HistoryEntry{
			{Role: "system", Content: "Starting tunnel..."},
			{Role: "tool_call", ToolID: "tool-1", ToolName: "bash", ToolDisplayName: "Run bash", ToolArgs: `{"command":"sleep 1"}`},
			{Role: "tool_result", ToolID: "tool-1", ToolName: "bash", Result: "done"},
			{Role: "assistant", Content: "All builds are running."},
		},
	}

	if tunnelSnapshotMatches(seeded, latest) {
		t.Fatal("expected changed live projection to force snapshot reseed")
	}
	if !tunnelSnapshotMatches(latest, latest) {
		t.Fatal("expected identical snapshots to match")
	}
}

func TestCurrentTunnelHistoryIncludesTerminalToolResultsWithoutBodies(t *testing.T) {
	cases := []struct {
		name     string
		status   chat.ToolStatus
		isError  bool
		toolName string
	}{
		{name: "success", status: chat.StatusSuccess, isError: false, toolName: "read_file"},
		{name: "error", status: chat.StatusError, isError: true, toolName: "read_file"},
		{name: "canceled", status: chat.StatusCanceled, isError: true, toolName: "read_file"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			item := chat.NewGenericToolItem("tool-1", tc.toolName, tc.status, "a.txt", m.chatStyles)
			item.SetResult("", tc.isError)
			m.chatWrite(item)

			history := m.currentTunnelHistory()
			if len(history) != 2 {
				t.Fatalf("expected tool_call and empty tool_result, got %+v", history)
			}
			if history[0].Role != "tool_call" {
				t.Fatalf("first entry role = %q, want tool_call", history[0].Role)
			}
			if history[1].Role != "tool_result" {
				t.Fatalf("second entry role = %q, want tool_result", history[1].Role)
			}
			if history[1].Result != "" {
				t.Fatalf("tool_result body = %q, want empty", history[1].Result)
			}
			if history[1].IsError != tc.isError {
				t.Fatalf("tool_result is_error = %t, want %t", history[1].IsError, tc.isError)
			}
		})
	}
}

func TestCurrentTunnelHistoryIncludesEmptySpawnAgentResultWhenComplete(t *testing.T) {
	m := newTestModel()

	item := chat.NewAgentToolItem("agent-1", "Researcher", chat.StatusCanceled, m.chatStyles)
	item.SetResult("")
	m.chatWrite(item)

	history := m.currentTunnelHistory()
	if len(history) != 2 {
		t.Fatalf("expected spawn_agent tool_call and empty tool_result, got %+v", history)
	}
	if history[0].Role != "tool_call" || history[0].ToolName != "spawn_agent" {
		t.Fatalf("unexpected first history entry: %+v", history[0])
	}
	if history[1].Role != "tool_result" || history[1].ToolName != "spawn_agent" {
		t.Fatalf("unexpected second history entry: %+v", history[1])
	}
	if history[1].Result != "" {
		t.Fatalf("spawn_agent tool_result body = %q, want empty", history[1].Result)
	}
	if !history[1].IsError {
		t.Fatal("expected canceled spawn_agent tool_result to be marked as error")
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
	m.pushTunnelStatus(tunnel.StatusThinking, "processing")
	m.pushTunnelActivity("processing")
	m.pushTunnelCurrentStatus()
	m.pushTunnelCurrentActivity()
	m.pushTunnelCancel()
	m.pushSubAgentTunnelStreamText("sa-1", "text")
	m.pushSubAgentTunnelToolCall("sa-1", "t1", "tool", "Tool", "{}", "")
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

func TestPushTunnelCurrentStatusUsesBusyLifecycle(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.loading = true
	m.statusActivity = "Collecting project knowledge..."

	m.pushTunnelCurrentStatus()
	m.pushTunnelCurrentActivity()

	if len(m.session.TunnelEvents) != 2 {
		t.Fatalf("expected 2 tunnel events, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventStatus {
		t.Fatalf("expected status event, got %q", got)
	}
	var data tunnel.StatusData
	if err := json.Unmarshal(m.session.TunnelEvents[0].Data, &data); err != nil {
		t.Fatalf("unmarshal status data: %v", err)
	}
	if data.Status != tunnel.StatusBusy {
		t.Fatalf("expected busy status, got %+v", data)
	}
	if got := m.session.TunnelEvents[1].Type; got != tunnel.EventActivity {
		t.Fatalf("expected activity event, got %q", got)
	}
	var activity tunnel.ActivityData
	if err := json.Unmarshal(m.session.TunnelEvents[1].Data, &activity); err != nil {
		t.Fatalf("unmarshal activity data: %v", err)
	}
	if activity.Activity != "Collecting project knowledge..." {
		t.Fatalf("expected collecting activity, got %+v", activity)
	}
}

func TestStartAgentWithExpandPushesInitialTunnelBusyAndActivity(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.loading = true
	m.statusActivity = "Thinking..."
	_, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	t.Cleanup(func() {
		cancel()
		time.Sleep(200 * time.Millisecond)
	})
	m.agent = agent.NewAgent(&testStreamProvider{events: []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "hello"},
		{Type: provider.StreamEventDone},
	}}, toolpkg.NewRegistry(), "", 1)

	cmd := m.startAgentWithExpand("hello")
	if cmd == nil {
		t.Fatal("expected startAgentWithExpand command")
	}
	cmd()

	deadline := time.Now().Add(2 * time.Second)
	for len(m.session.TunnelEvents) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(m.session.TunnelEvents) < 2 {
		t.Fatalf("expected initial tunnel status/activity events, got %d", len(m.session.TunnelEvents))
	}

	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventStatus {
		t.Fatalf("expected first event %q, got %q", tunnel.EventStatus, got)
	}
	var status tunnel.StatusData
	if err := json.Unmarshal(m.session.TunnelEvents[0].Data, &status); err != nil {
		t.Fatalf("unmarshal status data: %v", err)
	}
	if status.Status != tunnel.StatusBusy {
		t.Fatalf("expected busy status, got %+v", status)
	}

	if got := m.session.TunnelEvents[1].Type; got != tunnel.EventActivity {
		t.Fatalf("expected second event %q, got %q", tunnel.EventActivity, got)
	}
	var activity tunnel.ActivityData
	if err := json.Unmarshal(m.session.TunnelEvents[1].Data, &activity); err != nil {
		t.Fatalf("unmarshal activity data: %v", err)
	}
	if activity.Activity != "Thinking..." {
		t.Fatalf("expected thinking activity, got %+v", activity)
	}
}

func TestPushTunnelEventDoneDoesNotFlipMainAgentIdleMidLoop(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.loading = true
	m.statusActivity = "Working..."
	m.pushTunnelCurrentStatus()
	m.pushTunnelCurrentActivity()

	m.pushTunnelEvent(provider.StreamEvent{
		Type: provider.StreamEventToolCallDone,
		Tool: provider.ToolCallDelta{
			ID:        "tool-1",
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"echo hi"}`),
		},
	})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventDone})

	for _, ev := range m.session.TunnelEvents {
		if ev.Type != tunnel.EventStatus {
			continue
		}
		var data tunnel.StatusData
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatalf("unmarshal status data: %v", err)
		}
		if data.Status == tunnel.StatusIdle {
			t.Fatalf("stream turn completion must not emit idle while loop is still running: %+v", data)
		}
	}
}

func TestCancelActiveRunEmitsCancelledToolResult(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.loading = true
	m.cancelFunc = func() {}
	m.chatStartTool(ToolStatusMsg{
		ToolID:      "tool-1",
		ToolName:    "read_file",
		DisplayName: "Read File",
		Detail:      "a.txt",
		Running:     true,
	})

	m.cancelActiveRun()

	found := false
	for _, ev := range m.session.TunnelEvents {
		if ev.Type != tunnel.EventToolResult {
			continue
		}
		var data tunnel.ToolResultData
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatalf("unmarshal tool result data: %v", err)
		}
		if data.ToolID == "tool-1" {
			found = true
			if data.Result != "Cancelled" {
				t.Fatalf("expected cancelled tool result, got %q", data.Result)
			}
			if !data.IsError {
				t.Fatal("expected cancelled tool result to be marked as error")
			}
		}
	}
	if !found {
		t.Fatal("expected cancelled tool result event")
	}
}

func newTunnelRecordingModel(t *testing.T) *Model {
	t.Helper()

	m := newTestModel()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	ses := session.NewSession("", "", "")
	m.SetSession(ses, store)

	sess := tunnel.NewSession(tunnel.DefaultRelayURL)
	broker := tunnel.NewBroker(sess)
	broker.Stop()
	broker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
		m.recordTunnelEvent(ev)
	})
	m.tunnelBroker = broker
	m.tunnelSpawned = make(map[string]bool)
	return &m
}

func TestHandleSubAgentUpdateMsgPushesTunnelLifecycle(t *testing.T) {
	m := newTunnelRecordingModel(t)
	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	agentID := mgr.Spawn("reviewer", "review code", "review code", nil, context.Background())
	mgr.SetCancel(agentID, func() {})

	next, _ := m.handleSubAgentUpdateMsg(subAgentUpdateMsg{AgentID: agentID})
	m = &next

	if len(m.session.TunnelEvents) < 2 {
		t.Fatalf("expected lifecycle events, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventSubagentSpawn {
		t.Fatalf("expected first event %q, got %q", tunnel.EventSubagentSpawn, got)
	}
	if got := m.session.TunnelEvents[1].Type; got != tunnel.EventSubagentStatus {
		t.Fatalf("expected second event %q, got %q", tunnel.EventSubagentStatus, got)
	}
}

func TestHandleSubAgentTunnelToolMsgsPushEvents(t *testing.T) {
	m := newTunnelRecordingModel(t)

	next, _ := m.handleSubAgentTunnelToolCallMsg(subAgentTunnelToolCallMsg{
		AgentID:  "sa-1",
		ToolID:   "tool-1",
		ToolName: "read_file",
		Args:     `{"path":"a.txt"}`,
		Detail:   "a.txt",
	})
	m = &next
	next, _ = m.handleSubAgentTunnelToolResultMsg(subAgentTunnelToolResultMsg{
		AgentID:  "sa-1",
		ToolID:   "tool-1",
		ToolName: "read_file",
		Result:   "ok",
	})
	m = &next

	if len(m.session.TunnelEvents) != 2 {
		t.Fatalf("expected 2 tunnel events, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventSubagentToolCall {
		t.Fatalf("expected tool call event, got %q", got)
	}
	if got := m.session.TunnelEvents[1].Type; got != tunnel.EventSubagentToolResult {
		t.Fatalf("expected tool result event, got %q", got)
	}
}

func TestHandleSubAgentTunnelReasoningMsgPushesAndFinalizes(t *testing.T) {
	m := newTunnelRecordingModel(t)

	next, _ := m.handleSubAgentTunnelReasoningMsg(subAgentTunnelReasoningMsg{
		AgentID: "sa-1",
		Text:    "thinking",
	})
	m = &next
	next, _ = m.handleSubAgentTunnelToolCallMsg(subAgentTunnelToolCallMsg{
		AgentID:  "sa-1",
		ToolID:   "tool-1",
		ToolName: "read_file",
		Args:     `{"path":"a.txt"}`,
		Detail:   "a.txt",
	})
	m = &next

	if len(m.session.TunnelEvents) != 3 {
		t.Fatalf("expected 3 tunnel events, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventSubagentReasoning {
		t.Fatalf("expected reasoning event, got %q", got)
	}
	if got := m.session.TunnelEvents[1].Type; got != tunnel.EventSubagentReasoningDone {
		t.Fatalf("expected reasoning_done event, got %q", got)
	}
	if got := m.session.TunnelEvents[2].Type; got != tunnel.EventSubagentToolCall {
		t.Fatalf("expected tool call event, got %q", got)
	}
}

func TestPushTunnelEventReasoningFinalizesBeforeText(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.tunnelMsgID = "msg-1"

	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventReasoning, Text: "thinking"})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "hello"})
	m.pushTunnelEvent(provider.StreamEvent{Type: provider.StreamEventDone})

	if len(m.session.TunnelEvents) < 4 {
		t.Fatalf("expected at least 4 tunnel events, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventReasoning {
		t.Fatalf("expected first event %q, got %q", tunnel.EventReasoning, got)
	}
	if got := m.session.TunnelEvents[1].Type; got != tunnel.EventReasoningDone {
		t.Fatalf("expected second event %q, got %q", tunnel.EventReasoningDone, got)
	}
}

func TestCurrentTunnelHistoryMarksShellMessages(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("printf hi", true); cmd == nil {
		t.Fatal("expected shell command to start")
	}
	m.appendShellChunk("\x1b[31mhi\x1b[0m\n")

	history := m.currentTunnelHistory()
	if len(history) < 2 {
		t.Fatalf("expected shell command and output in history, got %d entries", len(history))
	}
	if history[0].Role != "user" || history[0].Kind != tunnel.MessageKindShellCommand {
		t.Fatalf("unexpected shell command history entry: %+v", history[0])
	}
	if history[0].Content != "$ printf hi" || history[0].DisplayText != "printf hi" {
		t.Fatalf("unexpected shell command content: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Kind != tunnel.MessageKindShellOutput {
		t.Fatalf("unexpected shell output history entry: %+v", history[1])
	}
	if !strings.Contains(history[1].Content, "hi") {
		t.Fatalf("expected shell output content, got %+v", history[1])
	}
}

func TestAppendShellChunkPushesShellOutputTextEvent(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.setShellMode(true)
	if cmd := m.submitShellCommand("printf hi", true); cmd == nil {
		t.Fatal("expected shell command to start")
	}
	m.appendShellChunk("hi\n")
	next, _ := m.handleShellCommandDoneMsg(shellCommandDoneMsg{
		RunID:  m.activeShellRunID,
		Status: toolpkg.CommandJobCompleted,
	})
	m = &next

	var sawShellCommand bool
	var sawShellOutput bool
	var sawShellDone bool
	for _, ev := range m.session.TunnelEvents {
		switch ev.Type {
		case tunnel.EventUserMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatalf("unmarshal user_message: %v", err)
			}
			if data.Kind == tunnel.MessageKindShellCommand {
				sawShellCommand = true
			}
		case tunnel.EventText:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatalf("unmarshal text: %v", err)
			}
			if data.Kind == tunnel.MessageKindShellOutput && strings.Contains(data.Chunk, "hi") {
				sawShellOutput = true
			}
		case tunnel.EventTextDone:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatalf("unmarshal text_done: %v", err)
			}
			if data.ID != "" {
				sawShellDone = true
			}
		}
	}
	if !sawShellCommand {
		t.Fatal("expected shell command user_message event")
	}
	if !sawShellOutput {
		t.Fatal("expected shell output text event")
	}
	if !sawShellDone {
		t.Fatal("expected shell output text_done event")
	}
}

func TestCronPromptPushesCronTunnelEventWithoutSystemEvent(t *testing.T) {
	m := newTunnelRecordingModel(t)

	next, _ := m.Update(cronPromptMsg{Prompt: "check status"})
	m2 := next.(Model)
	m = &m2

	var sawCron bool
	for _, ev := range m.session.TunnelEvents {
		switch ev.Type {
		case tunnel.EventUserMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatalf("unmarshal user_message: %v", err)
			}
			if data.Kind == tunnel.MessageKindCron {
				sawCron = true
				if data.Text != "check status" || data.DisplayText != "⏰ Cron job triggered" {
					t.Fatalf("unexpected cron message data: %+v", data)
				}
			}
		case tunnel.EventSystemMessage:
			t.Fatalf("did not expect cron to emit system_message tunnel event: %s", ev.Data)
		}
	}
	if !sawCron {
		t.Fatal("expected cron user_message tunnel event")
	}
}

func TestTurnMetricsDigestPushesTunnelSystemMessage(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.session.Metrics = []metrics.MetricEvent{
		{TurnIndex: 2, Type: "llm", TTFT: 1200 * time.Millisecond, ThinkTime: 2 * time.Second, Duration: 8 * time.Second},
		{TurnIndex: 2, Type: "tool", ToolName: "read_bash", ToolSuccess: false, ToolError: "timeout", ToolDuration: 3 * time.Second},
	}
	m.usageTurnIndex = 2

	m.appendTurnMetricsDigest(2)

	if len(m.session.TunnelEvents) != 1 {
		t.Fatalf("expected 1 tunnel event, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventSystemMessage {
		t.Fatalf("expected event %q, got %q", tunnel.EventSystemMessage, got)
	}

	var data tunnel.MessageData
	if err := json.Unmarshal(m.session.TunnelEvents[0].Data, &data); err != nil {
		t.Fatalf("unmarshal system_message: %v", err)
	}
	for _, want := range []string{"📊 Turn #2", "TTFT 1.2s", "Dur 8.0s", "Think 2.0s", "Tools 1", "Slowest read_bash 3.0s"} {
		if !strings.Contains(data.Text, want) {
			t.Fatalf("expected %q in metrics digest %q", want, data.Text)
		}
	}

	m.appendTurnMetricsDigest(2)
	if len(m.session.TunnelEvents) != 1 {
		t.Fatalf("expected metrics digest to emit once, got %d events", len(m.session.TunnelEvents))
	}
}

func TestHandleSubAgentTunnelToolCallMsgFillsDetailFallback(t *testing.T) {
	m := newTunnelRecordingModel(t)

	next, _ := m.handleSubAgentTunnelToolCallMsg(subAgentTunnelToolCallMsg{
		AgentID:  "sa-1",
		ToolID:   "tool-1",
		ToolName: "read_file",
		Args:     `{"path":"a.txt"}`,
	})
	m = &next

	if len(m.session.TunnelEvents) != 1 {
		t.Fatalf("expected 1 tunnel event, got %d", len(m.session.TunnelEvents))
	}

	var data tunnel.SubagentToolCallData
	if err := json.Unmarshal(m.session.TunnelEvents[0].Data, &data); err != nil {
		t.Fatalf("unmarshal tool call data: %v", err)
	}
	if data.Detail != "a.txt" {
		t.Fatalf("expected formatted detail %q, got %q", "a.txt", data.Detail)
	}
}

func TestHandleSwarmTunnelEventMsgPushesEvents(t *testing.T) {
	m := newTunnelRecordingModel(t)

	next, _ := m.handleSwarmTunnelEventMsg(swarmTunnelEventMsg{
		Event: swarm.Event{Type: "teammate_spawned", TeammateID: "tm-1", TeammateName: "reviewer"},
	})
	m = &next

	if len(m.session.TunnelEvents) != 1 {
		t.Fatalf("expected 1 tunnel event, got %d", len(m.session.TunnelEvents))
	}
	if got := m.session.TunnelEvents[0].Type; got != tunnel.EventSubagentSpawn {
		t.Fatalf("expected teammate spawn to normalize to %q, got %q", tunnel.EventSubagentSpawn, got)
	}
}

// ─── handleTunnelClientCommand nil-safety tests ───

func TestHandleTunnelClientCommand_InvalidJSON(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdMessage, Data: []byte("not json")}
	m.handleTunnelClientCommand(0, nil, cmd) // should not panic
}

func TestHandleTunnelClientCommand_EmptyText(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	data, _ := json.Marshal(tunnel.MessageData{Text: ""})
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdMessage, Data: data}
	m.handleTunnelClientCommand(0, nil, cmd) // should not panic
}

func TestHandleTunnelClientCommand_Interrupt(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdInterrupt}
	m.handleTunnelClientCommand(0, nil, cmd) // should not panic
}

func TestHandleTunnelClientCommand_ModeChange(t *testing.T) {
	m := newTestModel()
	m.tunnelBroker = nil
	data, _ := json.Marshal(tunnel.ModeChangeData{Mode: "auto"})
	cmd := tunnel.GatewayMessage{Type: tunnel.CmdModeChange, Data: data}
	m.handleTunnelClientCommand(0, nil, cmd) // should not panic
}

func TestHandleTunnelClientConnectedMsg_ClosesQROverlayAndWritesSystemMessage(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.tunnelSession = tunnel.NewSession(tunnel.DefaultRelayURL)
	m.openQROverlayDirect("Mobile Tunnel", "Scan with GGCode Mobile to connect", "QR", "wss://example")

	got, _ := m.handleTunnelClientConnectedMsg()
	updated := got.(*Model)

	if updated.qrOverlay != nil {
		t.Fatal("expected QR overlay to close after mobile connect")
	}
	rendered := stripAnsi(renderedOutput(updated))
	if !strings.Contains(rendered, updated.t("tunnel.mobile_connected")) {
		t.Fatalf("expected connected system message, got: %s", rendered)
	}
	if got := len(updated.session.TunnelEvents); got != 0 {
		t.Fatalf("expected local-only connected notice to stay out of tunnel history, got %d events", got)
	}
}

func TestHandleTunnelClientConnectedMsg_WritesNoticeOnlyOncePerShare(t *testing.T) {
	m := newTunnelRecordingModel(t)
	m.tunnelSession = tunnel.NewSession(tunnel.DefaultRelayURL)
	m.openQROverlayDirect("Mobile Tunnel", "Scan with GGCode Mobile to connect", "QR", "wss://example")

	got, _ := m.handleTunnelClientConnectedMsg()
	updated := got.(*Model)
	got, _ = updated.handleTunnelClientConnectedMsg()
	updated = got.(*Model)

	rendered := stripAnsi(renderedOutput(updated))
	if count := strings.Count(rendered, updated.t("tunnel.mobile_connected")); count != 1 {
		t.Fatalf("expected connected notice once per share, got %d in: %s", count, rendered)
	}
	if got := len(updated.session.TunnelEvents); got != 0 {
		t.Fatalf("expected duplicate connected notices to stay out of tunnel history, got %d events", got)
	}
}

func TestHandleTunnelClientConnectedMsg_IgnoresInactiveTunnel(t *testing.T) {
	m := newTestModel()
	m.openQROverlayDirect("Mobile Tunnel", "Scan with GGCode Mobile to connect", "QR", "wss://example")

	got, _ := m.handleTunnelClientConnectedMsg()
	updated := got.(*Model)

	if updated.qrOverlay == nil {
		t.Fatal("expected QR overlay to remain when tunnel is inactive")
	}
	rendered := stripAnsi(renderedOutput(updated))
	if strings.Contains(rendered, updated.t("tunnel.mobile_connected")) {
		t.Fatalf("did not expect connected system message without active tunnel, got: %s", rendered)
	}
}

func TestHandleTunnelClientConnectedMsg_IgnoresStaleGeneration(t *testing.T) {
	m := newTestModel()
	m.tunnelGeneration = 2
	m.tunnelSession = tunnel.NewSession(tunnel.DefaultRelayURL)
	m.openQROverlayDirect("Mobile Tunnel", "Scan with GGCode Mobile to connect", "QR", "wss://example")

	got, _ := m.handleTunnelClientConnectedMsgForGeneration(1)
	updated := got.(*Model)

	if updated.qrOverlay == nil {
		t.Fatal("expected stale connect event to leave QR overlay open")
	}
	rendered := stripAnsi(renderedOutput(updated))
	if strings.Contains(rendered, updated.t("tunnel.mobile_connected")) {
		t.Fatalf("did not expect stale connect event to write system message, got: %s", rendered)
	}
}

func TestHandleTunnelInboundMsg_IgnoresStaleGeneration(t *testing.T) {
	m := newTestModel()
	m.tunnelGeneration = 2

	got, cmd := m.handleTunnelInboundMsg(tunnelInboundMsg{generation: 1, text: "hello from stale tunnel"})
	updated := got.(*Model)
	if cmd != nil {
		t.Fatal("expected stale inbound tunnel event to do nothing")
	}
	if updated.loading {
		t.Fatal("expected stale inbound tunnel event to not start loading")
	}
	rendered := stripAnsi(renderedOutput(updated))
	if strings.Contains(rendered, "hello from stale tunnel") {
		t.Fatalf("did not expect stale inbound text to render, got: %s", rendered)
	}
}

func TestHandleTunnelStartMsg_DoesNotSeedRelayBeforeClientConnect(t *testing.T) {
	m := newTestModel()
	store := newTestSessionStore(t)
	ses := session.NewSession("", "", "")
	m.SetSession(ses, store)
	m.chatWriteUser(nextChatID(), "hello")

	tunnelSession := tunnel.NewSession("wss://test.local")
	broker := tunnel.NewBroker(tunnelSession)
	defer broker.Stop()
	defer tunnelSession.Stop()

	info := &tunnel.SessionInfo{
		ConnectURL: "wss://test.local/ws?role=client&token=test",
		QRCode:     "QR",
	}
	got, _ := m.handleTunnelStartMsg(tunnelStartMsg{
		info:    info,
		session: tunnelSession,
		broker:  broker,
	})
	updated := got.(*Model)

	if updated.qrOverlay == nil {
		t.Fatal("expected QR overlay to open after tunnel start")
	}
	if updated.tunnelBroker.SessionID() != ses.ID {
		t.Fatalf("expected broker session id to bind immediately, got %q want %q", updated.tunnelBroker.SessionID(), ses.ID)
	}
	if len(updated.session.TunnelEvents) != 0 {
		t.Fatalf("expected share start to avoid eager relay seeding, got %d recorded events", len(updated.session.TunnelEvents))
	}
}

func TestHandleTunnelStartMsg_RevalidatesResumedReplayLedgerBeforeClientConnect(t *testing.T) {
	m := newTestModel()
	store := newTestSessionStore(t)
	m.sessionStore = store

	ses := &session.Session{
		ID:        "sess-restart-share",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("before restart")}},
			{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("before restart reply")}},
		},
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{
				EventID: "ev-000000001",
				Type:    tunnel.EventUserMessage,
				Data:    json.RawMessage(`{"text":"before restart"}`),
			},
			{
				EventID:  "ev-000000002",
				StreamID: "msg-1",
				Type:     tunnel.EventText,
				Data:     json.RawMessage(`{"id":"msg-1","chunk":"before restart reply"}`),
			},
			{
				EventID:  "ev-000000003",
				StreamID: "msg-1",
				Type:     tunnel.EventTextDone,
				Data:     json.RawMessage(`{"id":"msg-1","done":true}`),
			},
		},
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}

	m.applyResumedSession(ses)
	m.chatWriteUser("user-after-restart", "after restart")
	after := chat.NewAssistantItem("assistant-after-restart", m.chatStyles)
	after.SetText("after restart reply")
	after.SetFinished()
	m.chatList.Append(after)

	tunnelSession := tunnel.NewSession("wss://test.local")
	broker := tunnel.NewBroker(tunnelSession)
	defer broker.Stop()
	defer tunnelSession.Stop()

	got, _ := m.handleTunnelStartMsg(tunnelStartMsg{
		info: &tunnel.SessionInfo{
			ConnectURL: "wss://test.local/ws?role=client&token=test",
			QRCode:     "QR",
		},
		session: tunnelSession,
		broker:  broker,
	})
	updated := got.(*Model)

	if updated.session.TunnelEventsComplete {
		t.Fatal("expected stale canonical replay ledger to be downgraded on share start")
	}
	if got := len(updated.currentSessionTunnelReplayEvents()); got != 0 {
		t.Fatalf("expected stale canonical replay to be disabled, got %d events", got)
	}
	snapshot := updated.tunnelSnapshot()
	if len(snapshot.History) != 4 {
		t.Fatalf("expected current snapshot history to include post-restart messages, got %d entries: %+v", len(snapshot.History), snapshot.History)
	}
	if snapshot.History[2].Role != "user" || snapshot.History[2].Content != "after restart" {
		t.Fatalf("expected post-restart user message in snapshot, got %+v", snapshot.History[2])
	}
	if snapshot.History[3].Role != "assistant" || snapshot.History[3].Content != "after restart reply" {
		t.Fatalf("expected post-restart assistant message in snapshot, got %+v", snapshot.History[3])
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if loaded.TunnelEventsComplete {
		t.Fatal("expected downgraded replay flag to persist")
	}
	if len(loaded.TunnelEvents) != 0 {
		t.Fatalf("expected stale replay events to be cleared, got %d", len(loaded.TunnelEvents))
	}
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

func TestHandleTunnelCommand_ShareRefreshesExistingTunnel(t *testing.T) {
	m := newTestModel()
	m.tunnelSession = tunnel.NewSession(tunnel.DefaultRelayURL)

	cmd := m.handleTunnelCommand("/share")

	if cmd == nil {
		t.Fatal("expected refresh command for active tunnel")
	}
}

func TestHandleTunnelCommand_DoesNotStartWhileStarting(t *testing.T) {
	m := newTestModel()
	m.tunnelStarting = true

	cmd := m.handleTunnelCommand("/share")

	if cmd != nil {
		t.Fatal("expected no duplicate start command while tunnel is already starting")
	}
	rendered := stripAnsi(renderedOutput(&m))
	if strings.Contains(rendered, "Starting tunnel...") {
		t.Fatalf("did not expect duplicate starting message, got: %s", rendered)
	}
}

func TestHandleTunnelRefreshMsgOpensOverlay(t *testing.T) {
	m := newTestModel()
	sess := tunnel.NewSession(tunnel.DefaultRelayURL)
	m.tunnelSession = sess
	m.tunnelGeneration = 2

	got, _ := m.handleTunnelRefreshMsg(tunnelRefreshMsg{
		generation: 2,
		session:    sess,
		info: &tunnel.SessionInfo{
			ConnectURL:          "wss://test.local/ws?role=client&auth_ticket=fresh",
			QRCode:              "qr",
			CompatibilityNotice: "fresh",
		},
	})
	updated := got.(*Model)
	if updated.qrOverlay == nil {
		t.Fatal("expected QR overlay to open")
	}
	if updated.qrOverlay.footer != "wss://test.local/ws?role=client&auth_ticket=fresh" {
		t.Fatalf("unexpected QR overlay URL: %+v", updated.qrOverlay)
	}
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

func TestHandleTunnelAskUserResponseDeliversPendingAnswer(t *testing.T) {
	m := newTestModel()
	respCh := make(chan toolpkg.AskUserResponse, 1)
	req := toolpkg.AskUserRequest{
		Title: "Clarify",
		Questions: []toolpkg.AskUserQuestion{
			{
				ID:     "notes",
				Title:  "Notes",
				Prompt: "Anything else?",
				Kind:   toolpkg.AskUserKindText,
			},
		},
	}
	m.pendingQuestionnaire = newQuestionnaireState(req, respCh, m.currentLanguage())
	m.tunnelPendingAskUserID = "ask-1"

	got, _ := m.handleTunnelAskUserResponse(tunnelAskUserResponseMsg{
		id:     "ask-1",
		status: toolpkg.AskUserStatusSubmitted,
		answers: []tunnel.AskUserAnswer{
			{QuestionID: "notes", FreeformText: "Ship it"},
		},
	})
	updated := got.(*Model)
	if updated.pendingQuestionnaire != nil {
		t.Fatal("expected pending questionnaire to be cleared")
	}
	if updated.tunnelPendingAskUserID != "" {
		t.Fatalf("expected pending ask user id to be cleared, got %q", updated.tunnelPendingAskUserID)
	}

	select {
	case resp := <-respCh:
		if resp.Status != toolpkg.AskUserStatusSubmitted {
			t.Fatalf("expected submitted status, got %q", resp.Status)
		}
		if len(resp.Answers) != 1 || resp.Answers[0].FreeformText != "Ship it" {
			t.Fatalf("unexpected ask user response: %+v", resp)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for ask user response delivery")
	}
}
