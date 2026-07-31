package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCompactOldReasoningBlocks(t *testing.T) {
	m := NewManager(100000)

	// Old assistant message with large reasoning content
	longReasoning := strings.Repeat("thinking about the problem ", 100) // ~2600 chars
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "thinking", ReasoningContent: longReasoning, ThinkingSignature: "sig-abc"},
			{Type: "text", Text: "Let me create the file."},
			{Type: "tool_use", ToolName: "write_file", ToolID: "t1", Input: []byte(`{"path":"a.go"}`)},
		},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", Output: "done"},
		},
	})

	// Most recent assistant message with reasoning — should be protected
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "thinking", ReasoningContent: strings.Repeat("recent reasoning ", 50), ThinkingSignature: "sig-recent"},
			{Type: "text", Text: "Done."},
		},
	})

	beforeTokens := m.TokenCount()
	freed := m.CompactOldReasoningBlocks()
	afterTokens := m.TokenCount()

	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}
	if afterTokens >= beforeTokens {
		t.Fatalf("expected token decrease, before=%d after=%d", beforeTokens, afterTokens)
	}

	msgs := m.Messages()
	// Old reasoning block should be compacted
	oldBlock := msgs[0].Content[0]
	if !strings.HasPrefix(oldBlock.ReasoningContent, "[compacted:") {
		t.Fatalf("expected old reasoning compacted, got %q", oldBlock.ReasoningContent)
	}
	// ThinkingSignature must be preserved
	if oldBlock.ThinkingSignature != "sig-abc" {
		t.Fatalf("expected ThinkingSignature preserved, got %q", oldBlock.ThinkingSignature)
	}
	// Most recent reasoning should NOT be compacted
	recentBlock := msgs[2].Content[0]
	if strings.HasPrefix(recentBlock.ReasoningContent, "[compacted:") {
		t.Fatalf("expected recent reasoning preserved, got %q", recentBlock.ReasoningContent)
	}
	if recentBlock.ThinkingSignature != "sig-recent" {
		t.Fatalf("expected recent ThinkingSignature preserved")
	}
}

func TestCompactOldReasoningBlocksIdempotent(t *testing.T) {
	m := NewManager(100000)
	longReasoning := strings.Repeat("reasoning ", 100)

	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "thinking", ReasoningContent: longReasoning, ThinkingSignature: "sig-1"},
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "latest"},
		},
	})

	first := m.CompactOldReasoningBlocks()
	if first <= 0 {
		t.Fatalf("expected first call to free tokens, got %d", first)
	}
	second := m.CompactOldReasoningBlocks()
	if second != 0 {
		t.Fatalf("expected second call to free 0 (idempotent), got %d", second)
	}
}

func TestCompactOldReasoningBlocksEmpty(t *testing.T) {
	m := NewManager(100000)
	if freed := m.CompactOldReasoningBlocks(); freed != 0 {
		t.Fatalf("expected 0 freed on empty manager, got %d", freed)
	}
}

func TestCompactOldReasoningBlocksDeepSeek(t *testing.T) {
	m := NewManager(100000)
	longReasoning := strings.Repeat("DeepSeek reasoning step ", 100)

	// DeepSeek-style: type "text" with ReasoningContent, no ThinkingSignature
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", ReasoningContent: longReasoning},
			{Type: "text", Text: "Here's the code."},
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "Done."},
		},
	})

	freed := m.CompactOldReasoningBlocks()
	if freed <= 0 {
		t.Fatalf("expected freed > 0 for DeepSeek reasoning, got %d", freed)
	}

	msgs := m.Messages()
	block := msgs[0].Content[0]
	if !strings.HasPrefix(block.ReasoningContent, "[compacted:") {
		t.Fatalf("expected DeepSeek reasoning compacted, got %q", block.ReasoningContent)
	}
}

func TestCompactOldReasoningBlocksSkipsShort(t *testing.T) {
	m := NewManager(100000)

	// Short reasoning below threshold should be skipped
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "thinking", ReasoningContent: "short reasoning", ThinkingSignature: "sig"},
		},
	})
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "latest"},
		},
	})

	freed := m.CompactOldReasoningBlocks()
	if freed != 0 {
		t.Fatalf("expected 0 freed for short reasoning, got %d", freed)
	}
}
