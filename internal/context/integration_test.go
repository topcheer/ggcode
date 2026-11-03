//go:build integration_local

package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestAutoSummarize_AtThreshold(t *testing.T) {
	cm := NewManager(1000)
	ctx := context.Background()
	prov := &mockProvider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt"}}})

	for i := 0; i < 8; i++ {
		cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 100)}}})
		cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("y", 100)}}})
	}

	ratio := cm.UsageRatio()
	t.Logf("Usage ratio: %.2f", ratio)

	beforeCount := len(cm.Messages())
	err := cm.Summarize(ctx, prov)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	afterCount := len(cm.Messages())
	t.Logf("Messages before: %d, after: %d", beforeCount, afterCount)

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

func TestSetMaxTokens(t *testing.T) {
	cm := NewManager(1000)

	if cm.ContextWindow() != 1000 {
		t.Errorf("Expected MaxTokens=1000, got %d", cm.ContextWindow())
	}

	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("x", 400)}}})

	ratio1000 := cm.UsageRatio()

	cm.SetContextWindow(2000)
	if cm.ContextWindow() != 2000 {
		t.Errorf("Expected MaxTokens=2000 after update, got %d", cm.ContextWindow())
	}

	ratio2000 := cm.UsageRatio()

	if ratio2000 >= ratio1000 {
		t.Errorf("Usage ratio should decrease when max tokens increases: ratio1000=%.3f, ratio2000=%.3f", ratio1000, ratio2000)
	}

	expectedRatio := ratio1000 / 2.0
	if ratio2000 < expectedRatio*0.9 || ratio2000 > expectedRatio*1.1 {
		t.Errorf("ratio2000 (%.3f) should be approximately half of ratio1000 (%.3f)", ratio2000, ratio1000)
	}
}

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
