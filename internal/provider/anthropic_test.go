package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicBuildParamsMarshalsValidToolUseInput(t *testing.T) {
	p := &AnthropicProvider{model: "test-model", maxTokens: 128}
	params := p.buildParams(context.Background(), []Message{
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

func TestAnthropicBuildParamsImageRoutingFallback(t *testing.T) {
	// Regression pin for the ctx-aware buildParams: with the Files uploader
	// unavailable (nil), image blocks in user messages and tool_results must
	// degrade to inline base64 sources and marshal cleanly.
	tiny := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	p := &AnthropicProvider{model: "test-model", maxTokens: 128}
	params := p.buildParams(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{{Type: "image", ImageMIME: "image/png", ImageData: tiny}}},
		{
			Role: "user",
			Content: []ContentBlock{{
				Type:   "tool_result",
				ToolID: "tool-1",
				Images: []ContentImage{{MIME: "image/png", Base64: tiny}},
			}},
		},
	}, nil)

	if len(params.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(params.Messages))
	}
	img := params.Messages[0].Content[0].OfImage
	if img == nil || img.Source.OfBase64 == nil {
		t.Fatal("user image should stay inline base64 when uploader is nil")
	}
	tr := params.Messages[1].Content[0].OfToolResult
	if tr == nil || len(tr.Content) != 1 || tr.Content[0].OfImage == nil || tr.Content[0].OfImage.Source.OfBase64 == nil {
		t.Fatal("tool_result image should stay inline base64 when uploader is nil")
	}
	if _, err := json.Marshal(params); err != nil {
		t.Fatalf("expected params to marshal, got %v", err)
	}
}

func TestAnthropicBuildParamsFallsBackForInvalidToolUseInput(t *testing.T) {
	// Truncated JSON that can be repaired (missing closing brace).
	// normalizeToolInputValue should repair it to valid JSON.
	p := &AnthropicProvider{model: "test-model", maxTokens: 128}
	params := p.buildParams(context.Background(), []Message{
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
	params := p.buildParams(context.Background(), []Message{
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

	params := p.buildParams(context.Background(), nil, nil)

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
	params := p.buildParams(context.Background(), nil, nil)

	if params.Thinking.OfEnabled != nil {
		t.Fatal("expected thinking config to be nil when effort is not set")
	}
}

// TestAnthropicMemoryToolDeclaration pins the memory_20250818 declaration
// contract: absent by default, exactly one memory tool in the union when
// enabled, and a cache breakpoint on it when it is the trailing static
// declaration (no server tools).
func TestAnthropicMemoryToolDeclaration(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}

	params := p.buildParams(context.Background(), nil, nil)
	for _, u := range params.Tools {
		if u.OfMemoryTool20250818 != nil {
			t.Fatal("memory tool must not be declared by default")
		}
	}

	p.SetMemoryTool(true)
	params = p.buildParams(context.Background(), nil, nil)
	count := 0
	for _, u := range params.Tools {
		if u.OfMemoryTool20250818 == nil {
			continue
		}
		count++
		if u.OfMemoryTool20250818.CacheControl.Type == "" {
			t.Fatal("expected cache breakpoint on the trailing memory declaration")
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 memory tool declaration, got %d", count)
	}
}
