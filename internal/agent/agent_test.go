package agent

import (
	"context"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// mockProvider is a simple mock for testing agent basics.
type mockProvider struct {
	chatResp *provider.ChatResponse
	chatErr  error
}

func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return m.chatResp, m.chatErr
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

func (m *mockProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
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
