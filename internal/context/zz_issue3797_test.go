package context

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue3797_TruncationRemovesFullTailCount(t *testing.T) {
	m := NewManager(100000)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "go"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "r1"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.ToolResultBlock("t1", "o", false)}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.ToolResultBlock("t2", "o", false)}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "r2"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{provider.ToolResultBlock("t3", "o", false)}})
	oldLen := len(m.Messages())

	if got := m.RemoveLastAssistantGroup(); got != "go" {
		t.Fatalf("prompt = %q", got)
	}
	remaining := len(m.Messages())
	wantRemoved := oldLen - remaining
	if wantRemoved != 5 {
		t.Fatalf("fixture: expected 5 removed messages, math says %d", wantRemoved)
	}
	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
}
