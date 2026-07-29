package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicBuildParamsMarshalsValidToolUseInput(t *testing.T) {
	p := &AnthropicProvider{model: "test-model", maxTokens: 128}
	params := p.buildParams([]Message{
		{
			Role: "assistant",
			Content: []ContentBlock{
				ToolUseBlock("tool-1", "edit_file", json.RawMessage(`{"path":"README.md","old_text":"a","new_text":"b"}`)),
			},
		},
		{
			Role:    "user",
			Content: []ContentBlock{ToolResultBlock("tool-1", "updated", false)},
		},
	}, nil)

	if _, err := json.Marshal(params); err != nil {
		t.Fatalf("expected anthropic params to marshal, got %v", err)
	}
}

func TestAnthropicBuildParamsFallsBackForInvalidToolUseInput(t *testing.T) {
	// Truncated JSON that can be repaired (missing closing brace).
	// normalizeToolInputValue should repair it to valid JSON.
	p := &AnthropicProvider{model: "test-model", maxTokens: 128}
	params := p.buildParams([]Message{
		{
			Role: "assistant",
			Content: []ContentBlock{
				ToolUseBlock("tool-1", "edit_file", json.RawMessage(`{"path":"README.md"`)),
			},
		},
		{
			Role:    "user",
			Content: []ContentBlock{ToolResultBlock("tool-1", "updated", false)},
		},
	}, nil)

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("expected anthropic params to marshal with repaired input, got %v", err)
	}
	// Repaired JSON should contain the valid path, not the raw fallback.
	if !strings.Contains(string(data), "README.md") {
		t.Fatalf("expected repaired path in marshaled params, got %s", string(data))
	}
	if strings.Contains(string(data), "_ggcode_raw_input") {
		t.Fatalf("repairable JSON should not fall back to raw input, got %s", string(data))
	}
}

func TestAnthropicBuildParamsFallsBackForUnrepairableInput(t *testing.T) {
	// Truly garbled input that cannot be repaired.
	p := &AnthropicProvider{model: "test-model", maxTokens: 128}
	params := p.buildParams([]Message{
		{
			Role: "assistant",
			Content: []ContentBlock{
				ToolUseBlock("tool-1", "edit_file", json.RawMessage(`<<garbage>>`)),
			},
		},
		{
			Role:    "user",
			Content: []ContentBlock{ToolResultBlock("tool-1", "updated", false)},
		},
	}, nil)

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("expected anthropic params to marshal with fallback input, got %v", err)
	}
	if !strings.Contains(string(data), "_ggcode_raw_input") {
		t.Fatalf("expected fallback marker for unrepairable input, got %s", string(data))
	}
}

func TestThinkingBudgetForEffort(t *testing.T) {
	tests := []struct {
		name      string
		effort    string
		maxTokens int
		want      int64
	}{
		{"disabled", "", 64000, 0},
		{"invalid", "turbo", 64000, 0},
		{"too_small", "low", 1024, 0},
		{"low_capped", "low", 64000, 5000},
		{"medium_capped", "medium", 64000, 16000},
		{"high_capped", "high", 64000, 32000},
		{"low_small_window", "low", 4096, 1024},
		{"high_small_window", "high", 4096, 2048},
		{"medium_8k", "medium", 8000, 3200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &AnthropicProvider{maxTokens: tt.maxTokens}
			got := p.thinkingBudgetForEffort(tt.effort)
			if got != tt.want {
				t.Errorf("thinkingBudgetForEffort(%q, maxTokens=%d) = %d, want %d", tt.effort, tt.maxTokens, got, tt.want)
			}
		})
	}
}

func TestAnthropicSetReasoningEffort(t *testing.T) {
	p := &AnthropicProvider{maxTokens: 64000}

	// Default is empty
	if p.ReasoningEffort() != "" {
		t.Fatalf("expected empty default effort, got %q", p.ReasoningEffort())
	}

	// Set valid effort
	p.SetReasoningEffort("high")
	if p.ReasoningEffort() != "high" {
		t.Fatalf("expected 'high', got %q", p.ReasoningEffort())
	}

	// Invalid effort is ignored
	p.SetReasoningEffort("turbo")
	if p.ReasoningEffort() != "high" {
		t.Fatalf("invalid effort should be ignored, got %q", p.ReasoningEffort())
	}

	// Empty disables
	p.SetReasoningEffort("")
	if p.ReasoningEffort() != "" {
		t.Fatalf("expected empty after disable, got %q", p.ReasoningEffort())
	}
}

func TestAnthropicBuildParamsWithThinking(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}
	p.SetReasoningEffort("medium")

	params := p.buildParams(nil, nil)

	if params.Thinking.OfEnabled == nil {
		t.Fatal("expected thinking config to be enabled for medium effort")
	}
	budget := params.Thinking.GetBudgetTokens()
	if budget == nil || *budget != 16000 {
		t.Fatalf("expected budget 16000, got %v", budget)
	}
}

func TestAnthropicBuildParamsWithoutThinking(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}
	// No effort set — thinking should not be configured
	params := p.buildParams(nil, nil)

	if params.Thinking.OfEnabled != nil {
		t.Fatal("expected thinking config to be nil when effort is not set")
	}
}
