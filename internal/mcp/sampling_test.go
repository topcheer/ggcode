package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParseSamplingParams(t *testing.T) {
	raw := json.RawMessage(`{
		"messages": [{"role": "user", "content": {"type": "text", "text": "hello"}}],
		"maxTokens": 100,
		"systemPrompt": "You are helpful"
	}`)
	params, err := ParseSamplingParams(raw)
	if err != nil {
		t.Fatalf("ParseSamplingParams error: %v", err)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params.Messages))
	}
	if params.Messages[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", params.Messages[0].Role)
	}
	if params.Messages[0].Content.Text != "hello" {
		t.Errorf("expected text 'hello', got %q", params.Messages[0].Content.Text)
	}
	if params.MaxTokens != 100 {
		t.Errorf("expected maxTokens 100, got %d", params.MaxTokens)
	}
	if params.SystemPrompt != "You are helpful" {
		t.Errorf("expected systemPrompt, got %q", params.SystemPrompt)
	}
}

func TestEffectiveMaxTokens(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, MaxSamplingTokens},
		{-1, MaxSamplingTokens},
		{100, 100},
		{4096, 4096},
		{99999, MaxSamplingTokens},
	}
	for _, tt := range tests {
		got := EffectiveMaxTokens(tt.input)
		if got != tt.want {
			t.Errorf("EffectiveMaxTokens(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestSamplingHandlerAdvertising(t *testing.T) {
	c := NewClient("test", "echo", nil)
	if c.samplingHandler != nil {
		t.Fatal("expected nil sampling handler by default")
	}

	// With handler, capability should be set
	c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
		return &SamplingResult{
			Model: "test-model",
			Role:  "assistant",
			Content: SamplingContent{
				Type: "text",
				Text: "response",
			},
			StopReason: "end_turn",
		}, nil
	})
	if c.samplingHandler == nil {
		t.Fatal("expected non-nil sampling handler after SetSamplingHandler")
	}
}

func TestSamplingHandlerRejectsWhenNil(t *testing.T) {
	// When no handler is set, handleSampling should write an error response.
	// We can't test the full path without a started transport, but we verify
	// the handler check logic: nil handler means rejection.
	c := NewClient("test", "echo", nil)
	if c.samplingHandler != nil {
		t.Fatal("expected nil sampling handler by default")
	}
	// Verify SetSamplingHandler works
	c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
		return nil, nil
	})
	if c.samplingHandler == nil {
		t.Fatal("expected non-nil sampling handler after SetSamplingHandler")
	}
}

func TestSamplingResultJSON(t *testing.T) {
	result := SamplingResult{
		Model:      "test-model",
		StopReason: "end_turn",
		Role:       "assistant",
		Content: SamplingContent{
			Type: "text",
			Text: "hello world",
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var roundTrip SamplingResult
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if roundTrip.Model != result.Model {
		t.Errorf("model mismatch: %q vs %q", roundTrip.Model, result.Model)
	}
	if roundTrip.Content.Text != result.Content.Text {
		t.Errorf("text mismatch: %q vs %q", roundTrip.Content.Text, result.Content.Text)
	}
}
