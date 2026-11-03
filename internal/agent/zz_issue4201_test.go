package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestRunStreamTextWithZeroOutputTokensNotEmpty(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{{
			{Type: provider.StreamEventText, Text: "Here is your answer."},
			{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 0}},
		}},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)
	defer a.Close()

	var texts []string
	err := a.RunStream(context.Background(), "say hi", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventSystem || event.Type == provider.StreamEventText {
			texts = append(texts, event.Text)
		}
	})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if got := mp.streamCallCount(); got != 1 {
		t.Fatalf("expected exactly 1 stream call (no retry nudge), got %d", got)
	}
	joined := strings.Join(texts, "\n")
	if strings.Contains(joined, "previous response was empty") {
		t.Fatalf("normal text misread as empty response, nudge injected: %q", joined)
	}
	if !strings.Contains(joined, "Here is your answer.") {
		t.Fatalf("streamed text missing from output: %q", joined)
	}
}

func TestRunStreamGenuinelyEmptyStillRetried(t *testing.T) {
	mp := &mockProvider{
		streamEvents: [][]provider.StreamEvent{
			{{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 0}}},
			{{Type: provider.StreamEventText, Text: "recovered"},
				{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}}},
		},
	}
	a := NewAgent(mp, tool.NewRegistry(), "", 5)
	defer a.Close()

	err := a.RunStream(context.Background(), "say hi", func(event provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("RunStream() error = %v", err)
	}
	if got := mp.streamCallCount(); got != 2 {
		t.Fatalf("expected 2 stream calls (empty retry then recovery), got %d", got)
	}
}
