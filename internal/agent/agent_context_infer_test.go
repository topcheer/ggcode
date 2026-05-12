package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestContextOverflowInfersWindow verifies that when a context overflow error
// is received, the context window is automatically inferred and MaxTokens
// is immediately updated — affecting UsageRatio and all related metrics.
func TestContextOverflowInfersWindow(t *testing.T) {
	// Use an error message format that parseContextWindowFromError recognizes:
	// "limit of N tokens" matches pattern: limit\W+(?:of\s+)?(\d+)
	overflowErr := fmt.Errorf("request exceeds the limit of 128000 tokens")

	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Summary."}}},
		},
		streamEvents: [][]provider.StreamEvent{
			// First call: overflow error
			{
				{Type: provider.StreamEventError, Error: overflowErr},
				{Type: provider.StreamEventDone},
			},
			// Second call (retry after compact): success
			{
				{Type: provider.StreamEventText, Text: "Retry answer."},
				{Type: provider.StreamEventDone},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "System", 5)
	a.ContextManager().SetMaxTokens(200_000)
	a.SetProbeKey("testvendor|https://api.test.com|test-model")

	// Add messages to have some token count (will be small in test)
	for i := 0; i < 10; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("user message %d with padding content for token usage", i)}}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("assistant response %d with padding content for token usage", i)}}})
	}

	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "trigger overflow"}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("RunStreamWithContent() error = %v", err)
	}

	// Verify MaxTokens was reduced from 200K to 128K (inferred from error message)
	newMax := a.ContextManager().MaxTokens()
	if newMax != 128_000 {
		t.Errorf("expected MaxTokens reduced to 128000, got %d", newMax)
	}

	// Verify UsageRatio reflects the new window immediately
	ratio := a.ContextManager().UsageRatio()
	if ratio <= 0 {
		t.Errorf("expected positive UsageRatio, got %f", ratio)
	}
}

// TestContextOverflowInferFromEstimate tests inference when the error has no
// exact token limit — uses currentTokenCount to match nearest tier.
func TestContextOverflowInferFromEstimate(t *testing.T) {
	overflowErr := errors.New("request too large: input token count exceeds model limit")

	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Summary."}}},
		},
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventError, Error: overflowErr},
				{Type: provider.StreamEventDone},
			},
			{
				{Type: provider.StreamEventText, Text: "Retry."},
				{Type: provider.StreamEventDone},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "System", 5)
	a.ContextManager().SetMaxTokens(512_000)
	a.SetProbeKey("vendor|url|model")

	for i := 0; i < 10; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("user msg %d with padding content for token usage estimation", i)}}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("assistant resp %d with padding content for token usage", i)}}})
	}

	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "trigger overflow"}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("RunStreamWithContent() error = %v", err)
	}

	// Without exact value, uses currentTokenCount (~287) → matches to 64K (minimum tier)
	newMax := a.ContextManager().MaxTokens()
	if newMax >= 512_000 {
		t.Errorf("expected MaxTokens reduced below 512000, got %d", newMax)
	}
	if newMax < 64_000 {
		t.Errorf("expected MaxTokens >= 64000 (minimum tier), got %d", newMax)
	}
}

// TestContextOverflowNoProbeKey verifies inference is skipped without a probe key.
func TestContextOverflowNoProbeKey(t *testing.T) {
	overflowErr := fmt.Errorf("request exceeds the limit of 64000 tokens")

	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Summary."}}},
		},
		streamEvents: [][]provider.StreamEvent{
			{
				{Type: provider.StreamEventError, Error: overflowErr},
				{Type: provider.StreamEventDone},
			},
			{
				{Type: provider.StreamEventText, Text: "Retry."},
				{Type: provider.StreamEventDone},
			},
		},
	}

	a := NewAgent(mp, tool.NewRegistry(), "System", 5)
	a.ContextManager().SetMaxTokens(200_000)
	// Deliberately NOT calling SetProbeKey

	for i := 0; i < 10; i++ {
		a.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("msg %d", i)}}})
		a.AddMessage(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("resp %d", i)}}})
	}

	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "trigger"}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("RunStreamWithContent() error = %v", err)
	}

	// Without probeKey, MaxTokens should remain unchanged
	newMax := a.ContextManager().MaxTokens()
	if newMax != 200_000 {
		t.Errorf("expected MaxTokens unchanged at 200000 (no probe key), got %d", newMax)
	}
}
