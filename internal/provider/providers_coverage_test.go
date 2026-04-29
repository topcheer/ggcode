package provider

import (
	"errors"
	"testing"

	"google.golang.org/genai"
)

func TestAnthropicStopReasonError(t *testing.T) {
	tests := []struct {
		reason string
		nilErr bool
	}{
		{"end_turn", true},
		{"tool_use", true},
		{"stop_sequence", true},
		{"pause_turn", true},
		{"max_tokens", false},
		{"refusal", false},
		{"unknown_reason", false},
	}
	for _, tt := range tests {
		err := anthropicStopReasonError(tt.reason)
		if tt.nilErr && err != nil {
			t.Errorf("anthropicStopReasonError(%q) = %v, want nil", tt.reason, err)
		}
		if !tt.nilErr && err == nil {
			t.Errorf("anthropicStopReasonError(%q) = nil, want error", tt.reason)
		}
	}
}

func TestGeminiFinishReasonError(t *testing.T) {
	tests := []struct {
		reason genai.FinishReason
		nilErr bool
	}{
		{genai.FinishReasonStop, true},
		{genai.FinishReasonUnspecified, true},
		{genai.FinishReason(""), true},
		{genai.FinishReasonMaxTokens, false},
		{genai.FinishReasonSafety, false},
		{genai.FinishReasonRecitation, false},
		{genai.FinishReasonProhibitedContent, false},
		{genai.FinishReasonBlocklist, false},
		{genai.FinishReasonMalformedFunctionCall, false},
	}
	for _, tt := range tests {
		err := geminiFinishReasonError(tt.reason)
		if tt.nilErr && err != nil {
			t.Errorf("geminiFinishReasonError(%v) = %v, want nil", tt.reason, err)
		}
		if !tt.nilErr && err == nil {
			t.Errorf("geminiFinishReasonError(%v) = nil, want error", tt.reason)
		}
	}
}

func TestNewAnthropicProvider(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-4", 4096)
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected 'anthropic', got %q", p.Name())
	}
	if p.model != "claude-4" {
		t.Errorf("expected model 'claude-4', got %q", p.model)
	}
}

func TestNewAnthropicProviderWithBaseURL(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-4", 4096, "http://localhost:8080")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewOpenAIProvider(t *testing.T) {
	p := NewOpenAIProvider("test-key", "gpt-4", 4096)
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", p.Name())
	}
}

func TestNewOpenAIProviderWithBaseURL(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("test-key", "gpt-4", 4096, "http://localhost:8080/v1")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestNewGeminiProvider(t *testing.T) {
	p, err := NewGeminiProvider("test-key", "gemini-2.0-flash", 4096)
	if err != nil {
		t.Fatalf("NewGeminiProvider error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "gemini" {
		t.Errorf("expected 'gemini', got %q", p.Name())
	}
}

func TestNewCopilotProvider(t *testing.T) {
	p := NewCopilotProvider("test-token", "gpt-4", 4096, "")
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "github-copilot" {
		t.Errorf("expected 'copilot', got %q", p.Name())
	}
}

func TestMaxTokensRejection_Comprehensive(t *testing.T) {
	// Already tested but add more edge cases
	rejected, limit := maxTokensRejection(errors.New("context window exceeded"))
	if rejected {
		t.Error("expected false for context window")
	}
	rejected, limit = maxTokensRejection(errors.New("context length exceeded"))
	if rejected {
		t.Error("expected false for context length")
	}
	_, _ = rejected, limit
}

func TestAdaptiveCapOnTruncated(t *testing.T) {
	var c adaptiveCap
	c.key = "test"
	c.lo = 100
	c.hi = 4096
	c.cur.Store(2000)
	c.OnTruncated()
	// OnTruncated increases cap
	if c.cur.Load() <= 2000 {
		t.Errorf("expected cap > 2000 after truncate, got %d", c.cur.Load())
	}
}

func TestAdaptiveCapOnTruncated_MinClamp(t *testing.T) {
	var c adaptiveCap
	c.key = "test"
	c.lo = 50
	c.hi = 4096
	c.cur.Store(60)
	c.OnTruncated()
	// Should not go below lo
	if c.cur.Load() < int64(c.lo) {
		t.Errorf("cur %d below lo %d", c.cur.Load(), c.lo)
	}
}

func TestAdaptiveCapOnRejected(t *testing.T) {
	var c adaptiveCap
	c.key = "test"
	c.lo = 100
	c.hi = 4096
	c.cur.Store(2000)
	c.OnRejected(1500)
	// OnRejected steps down toward parsed limit
	if c.cur.Load() >= 2000 {
		t.Errorf("expected cap < 2000 after reject, got %d", c.cur.Load())
	}
}

func TestAdaptiveCapOnRejected_ClampLo(t *testing.T) {
	var c adaptiveCap
	c.key = "test"
	c.lo = 100
	c.hi = 4096
	c.cur.Store(2000)
	c.OnRejected(50) // below lo
	// Should clamp to lo
	if c.cur.Load() != int64(c.lo) {
		t.Errorf("expected %d, got %d", c.lo, c.cur.Load())
	}
}

func TestBuildHeadersForProvider(t *testing.T) {
	headers := BuildHeadersForProvider("anthropic")
	if headers == nil {
		t.Error("expected non-nil headers")
	}
	headers = BuildHeadersForProvider("openai")
	_ = headers
	headers = BuildHeadersForProvider("unknown")
	_ = headers
}
