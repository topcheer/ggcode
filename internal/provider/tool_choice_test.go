package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestAnthropicProvider_SetToolChoice(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-20250514", 4096)

	// Default should be empty
	if got := p.ToolChoice(); got != "" {
		t.Fatalf("expected empty tool_choice, got %q", got)
	}

	p.SetToolChoice("required")
	if got := p.ToolChoice(); got != "required" {
		t.Fatalf("expected %q, got %q", "required", got)
	}

	// Whitespace and case normalization
	p.SetToolChoice("  NONE  ")
	if got := p.ToolChoice(); got != "none" {
		t.Fatalf("expected %q, got %q", "none", got)
	}

	p.SetToolChoice("Auto")
	if got := p.ToolChoice(); got != "auto" {
		t.Fatalf("expected %q, got %q", "auto", got)
	}
}

func TestAnthropicProvider_ToolChoicePreservedInClone(t *testing.T) {
	p := NewAnthropicProvider("test-key", "claude-sonnet-4-20250514", 4096)
	p.SetToolChoice("required")

	clone := p.CloneWithModel("claude-sonnet-4-20250514")
	if got := clone.(*AnthropicProvider).ToolChoice(); got != "required" {
		t.Fatalf("expected cloned tool_choice %q, got %q", "required", got)
	}
}

func TestOpenAIProvider_SetToolChoice(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("test-key", "gpt-4o", 4096, "https://api.openai.com/v1")

	// Default should be empty
	if got := p.ToolChoice(); got != "" {
		t.Fatalf("expected empty tool_choice, got %q", got)
	}

	p.SetToolChoice("required")
	if got := p.ToolChoice(); got != "required" {
		t.Fatalf("expected %q, got %q", "required", got)
	}

	// Whitespace and case normalization
	p.SetToolChoice("  NONE  ")
	if got := p.ToolChoice(); got != "none" {
		t.Fatalf("expected %q, got %q", "none", got)
	}
}

func TestOpenAIProvider_ToolChoicePreservedInClone(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("test-key", "gpt-4o", 4096, "https://api.openai.com/v1")
	p.SetToolChoice("none")

	clone := p.CloneWithModel("gpt-4o-mini")
	if got := clone.(*OpenAIProvider).ToolChoice(); got != "none" {
		t.Fatalf("expected cloned tool_choice %q, got %q", "none", got)
	}
}

func TestOpenAIProvider_ApplyToolChoice(t *testing.T) {
	p := NewOpenAIProviderWithBaseURL("test-key", "gpt-4o", 4096, "https://api.openai.com/v1")

	// With no tools, tool_choice should not be set even if configured
	var req openai.ChatCompletionRequest
	p.SetToolChoice("required")
	p.applyToolChoice(&req)
	if req.ToolChoice != nil {
		t.Fatalf("expected nil tool_choice when no tools, got %v", req.ToolChoice)
	}

	// Add tools and test
	req.Tools = []openai.Tool{{}}
	p.applyToolChoice(&req)
	if got, ok := req.ToolChoice.(string); !ok || got != "required" {
		t.Fatalf("expected tool_choice %q, got %v", "required", req.ToolChoice)
	}

	// Empty tool_choice should not set anything
	req = openai.ChatCompletionRequest{}
	req.Tools = []openai.Tool{{}}
	p.SetToolChoice("")
	p.applyToolChoice(&req)
	if req.ToolChoice != nil {
		t.Fatalf("expected nil tool_choice when empty, got %v", req.ToolChoice)
	}
}
