package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestTruncationAutoContinue verifies that when a streaming response is
// truncated (finish_reason=length / max_tokens), the agent:
//  1. Keeps the partial content instead of discarding it as an error.
//  2. Auto-continues by injecting a continuation prompt.
//  3. Eventually completes when the model produces a full response.
func TestTruncationAutoContinue(t *testing.T) {
	// First stream: partial text + truncated Done (simulates max_tokens).
	// Second stream: full text + normal Done.
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			// Turn 1: truncated response
			{
				{Type: provider.StreamEventText, Text: "Here is the beginning of a long an"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 100}, Truncated: true},
			},
			// Turn 2: continuation that completes the response
			{
				{Type: provider.StreamEventText, Text: "swer that continues from where I left off."},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 15, OutputTokens: 20}, Truncated: false},
			},
		},
	}

	registry := tool.NewRegistry()
	a := NewAgent(mp, registry, "", 5)
	if a == nil {
		t.Fatal("NewAgent returned nil")
	}

	var collectedText strings.Builder
	err := a.RunStream(context.Background(), "Tell me a story", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			collectedText.WriteString(event.Text)
		}
	})

	if err != nil {
		t.Fatalf("Agent.Run returned error: %v", err)
	}

	// The agent should have made 2 stream calls (truncated + continuation).
	mp.mu.Lock()
	calls := mp.streamCalls
	mp.mu.Unlock()
	if calls != 2 {
		t.Errorf("expected 2 stream calls (truncated + continuation), got %d", calls)
	}

	// Both pieces of text should be present in the collected output.
	combined := collectedText.String()
	if !strings.Contains(combined, "beginning of a long an") {
		t.Errorf("partial text from truncated turn was lost; got: %q", combined)
	}
	if !strings.Contains(combined, "continues from where I left off") {
		t.Errorf("continuation text missing; got: %q", combined)
	}
}

// TestTruncationMaxRetries verifies that after 3 consecutive truncations,
// the agent gives up and returns rather than looping forever.
func TestTruncationMaxRetries(t *testing.T) {
	mp := &mockProvider{}
	// Fill with truncated responses for all turns.
	mp.streamEvents = make([][]provider.StreamEvent, 5)
	for i := range mp.streamEvents {
		mp.streamEvents[i] = []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "partial..."},
			{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 50}, Truncated: true},
		}
	}

	registry := tool.NewRegistry()
	a := NewAgent(mp, registry, "", 10)

	err := a.RunStream(context.Background(), "write a lot", func(event provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("Agent.Run returned error after max truncation retries: %v", err)
	}

	// Should stop after 3 truncation retries, not loop forever.
	mp.mu.Lock()
	calls := mp.streamCalls
	mp.mu.Unlock()
	if calls > 4 {
		t.Errorf("expected at most 4 stream calls (3 retries + 1), got %d — agent is looping", calls)
	}
}
