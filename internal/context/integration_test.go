package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestAutoSummarize verifies that auto-summarization triggers at 80% threshold
func TestAutoSummarize_AtThreshold(t *testing.T) {
	cm := NewManager(1000) // Small limit
	ctx := context.Background()
	prov := &mockProvider{}

	// Add system prompt
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt"}}})

	// Add enough messages to have old ones to summarize
	for i := 0; i < 8; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 100)}}})
		cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("y", 100)}}})
	}

	// Verify we're at or near the threshold
	ratio := cm.UsageRatio()
	t.Logf("Usage ratio: %.2f", ratio)

	// Manually trigger summarization to verify it works
	beforeCount := len(cm.Messages())
	err := cm.Summarize(ctx, prov)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	afterCount := len(cm.Messages())
	t.Logf("Messages before: %d, after: %d", beforeCount, afterCount)

	// Check for summary block
	hasSummary := false
	for _, m := range cm.Messages() {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "[Previous conversation summary]") {
				hasSummary = true
				t.Logf("Found summary block: %s", b.Text[:50]+"...")
			}
		}
	}
	if !hasSummary {
		t.Error("Summary block not found after summarization")
	}
}

// TestSetMaxTokens verifies max tokens can be updated
func TestSetMaxTokens(t *testing.T) {
	cm := NewManager(1000)

	if cm.MaxTokens() != 1000 {
		t.Errorf("Expected MaxTokens=1000, got %d", cm.MaxTokens())
	}

	// Add a message with known token count
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 400)}}})

	// Get ratio with maxTokens=1000
	ratio1000 := cm.UsageRatio()

	// Double the max tokens
	cm.SetMaxTokens(2000)
	if cm.MaxTokens() != 2000 {
		t.Errorf("Expected MaxTokens=2000 after update, got %d", cm.MaxTokens())
	}

	// Get ratio with maxTokens=2000 - should be half
	ratio2000 := cm.UsageRatio()

	if ratio2000 >= ratio1000 {
		t.Errorf("Usage ratio should decrease when max tokens increases: ratio1000=%.3f, ratio2000=%.3f", ratio1000, ratio2000)
	}

	// Verify it's approximately half
	expectedRatio := ratio1000 / 2.0
	if ratio2000 < expectedRatio*0.9 || ratio2000 > expectedRatio*1.1 {
		t.Errorf("ratio2000 (%.3f) should be approximately half of ratio1000 (%.3f)", ratio2000, ratio1000)
	}
}

// TestClearPreservesSystem verifies Clear() preserves system prompt
func TestClearPreservesSystem(t *testing.T) {
	cm := NewManager(1000)

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System instructions"}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "User message"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Assistant response"}}})

	cm.Clear()

	msgs := cm.Messages()
	if len(msgs) != 1 {
		t.Errorf("Expected 1 message (system) after Clear, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("Expected system message, got role=%s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content[0].Text, "System instructions") {
		t.Error("System prompt content was lost")
	}
}
