package agent

import (
	"context"
	"encoding/json"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

type mockTool struct {
	name   string
	result tool.Result
}

func (t mockTool) Name() string                { return t.name }
func (t mockTool) Description() string         { return "mock tool" }
func (t mockTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t mockTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	return t.result, nil
}

// mockProvider is a simple mock for testing agent basics.
type mockProvider struct {
	chatResp      *provider.ChatResponse
	chatResponses []*provider.ChatResponse
	chatErr       error
	tokenCount    int
	chatCalls     int
	streamCalls   int
}

func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.chatCalls++
	if len(m.chatResponses) > 0 {
		resp := m.chatResponses[0]
		m.chatResponses = m.chatResponses[1:]
		return resp, m.chatErr
	}
	return m.chatResp, m.chatErr
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	m.streamCalls++
	return nil, nil
}

func (m *mockProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return m.tokenCount, nil
}

func (m *mockProvider) Name() string { return "mock" }

func TestNewAgent(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "Hello!"}},
			},
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 5},
		},
	}
	registry := tool.NewRegistry()
	a := NewAgent(mp, registry, "Be helpful", 5)

	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
}

func TestAgent_AddMessage(t *testing.T) {
	mp := &mockProvider{}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "test"}},
	})

	msgs := a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role 'user', got %s", msgs[0].Role)
	}
}

func TestAgent_SystemPrompt(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "You are a test bot.", 1)
	msgs := a.ContextManager().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("expected system role, got %s", msgs[0].Role)
	}
}

func TestAgent_SetProvider(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	mp2 := &mockProvider{}
	a.SetProvider(mp2)
}

func TestAgent_ProviderAwareTokenCountingIsWired(t *testing.T) {
	a := NewAgent(&mockProvider{tokenCount: 7}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "test"}},
	})
	if got := a.ContextManager().TokenCount(); got != 7 {
		t.Fatalf("expected token count 7 from provider, got %d", got)
	}
}

func TestAgent_SetProviderUpdatesContextManager(t *testing.T) {
	a := NewAgent(&mockProvider{tokenCount: 2}, tool.NewRegistry(), "", 1)
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "one"}},
	})
	a.SetProvider(&mockProvider{tokenCount: 9})
	a.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "two"}},
	})
	if got := a.ContextManager().TokenCount(); got != 11 {
		t.Fatalf("expected mixed token count 11 after provider switch, got %d", got)
	}
}

func TestAgent_ContextManager(t *testing.T) {
	a := NewAgent(&mockProvider{}, tool.NewRegistry(), "", 1)
	cm := a.ContextManager()
	if cm == nil {
		t.Fatal("ContextManager is nil")
	}
	if cm.MaxTokens() != 128000 {
		t.Errorf("expected default MaxTokens 128000, got %d", cm.MaxTokens())
	}
}

func TestReplaceFirst(t *testing.T) {
	tests := []struct {
		s        string
		old      string
		new      string
		expected string
	}{
		{"hello world", "world", "go", "hello go"},
		{"aaa", "a", "b", "baa"},
		{"hello", "x", "y", "hello"},
		{"", "a", "b", ""},
	}
	for _, tt := range tests {
		got := replaceFirst(tt.s, tt.old, tt.new)
		if got != tt.expected {
			t.Errorf("replaceFirst(%q, %q, %q) = %q, want %q", tt.s, tt.old, tt.new, got, tt.expected)
		}
	}
}

func TestContextManagerTokenEstimation(t *testing.T) {
	cm := ctxpkg.NewManager(1000)
	cm.Add(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "你好世界hello"}},
	})
	if cm.TokenCount() == 0 {
		t.Error("TokenCount should not be 0 after adding a message")
	}
}

func TestRunStreamUsesNonStreamingChat(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role: "assistant",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "# Hello\n\nWorld"},
				},
			},
			Usage: provider.TokenUsage{InputTokens: 12, OutputTokens: 7},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 1)

	var events []provider.StreamEvent
	var gotUsage provider.TokenUsage
	a.SetUsageHandler(func(usage provider.TokenUsage) {
		gotUsage = usage
	})

	err := a.RunStream(context.Background(), "hi", func(event provider.StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	if mp.chatCalls != 1 {
		t.Fatalf("expected Chat to be called once, got %d", mp.chatCalls)
	}
	if mp.streamCalls != 0 {
		t.Fatalf("expected ChatStream to be unused, got %d calls", mp.streamCalls)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != provider.StreamEventText || events[0].Text != "# Hello\n\nWorld" {
		t.Fatalf("unexpected text event: %#v", events[0])
	}
	if events[1].Type != provider.StreamEventDone || events[1].Usage == nil {
		t.Fatalf("expected done event with usage, got %#v", events[1])
	}
	if gotUsage != (provider.TokenUsage{InputTokens: 12, OutputTokens: 7}) {
		t.Fatalf("unexpected usage callback: %#v", gotUsage)
	}
}

func TestRunStreamEmitsToolProgressFromChatResponse(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("call_1", "echo", []byte(`{"text":"hi"}`)),
					},
				},
				Usage: provider.TokenUsage{InputTokens: 20, OutputTokens: 4},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("done")},
				},
				Usage: provider.TokenUsage{InputTokens: 8, OutputTokens: 2},
			},
		},
	}
	registry := tool.NewRegistry()
	if err := registry.Register(mockTool{name: "echo", result: tool.Result{Content: "ok"}}); err != nil {
		t.Fatalf("register mock tool: %v", err)
	}

	a := NewAgent(mp, registry, "", 2)
	var events []provider.StreamEvent
	err := a.RunStream(context.Background(), "hi", func(event provider.StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}
	if mp.chatCalls != 2 {
		t.Fatalf("expected 2 chat calls, got %d", mp.chatCalls)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	if events[0].Type != provider.StreamEventToolCallDone || events[0].Tool.Name != "echo" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[1].Type != provider.StreamEventDone || events[1].Usage == nil || events[1].Usage.InputTokens != 20 {
		t.Fatalf("unexpected second event: %#v", events[1])
	}
	if events[2].Type != provider.StreamEventToolResult || events[2].Result != "ok" {
		t.Fatalf("unexpected tool result event: %#v", events[2])
	}
	if events[3].Type != provider.StreamEventText || events[3].Text != "done" {
		t.Fatalf("unexpected assistant text event: %#v", events[3])
	}
	if events[4].Type != provider.StreamEventDone || events[4].Usage == nil || events[4].Usage.OutputTokens != 2 {
		t.Fatalf("unexpected final done event: %#v", events[4])
	}
}
