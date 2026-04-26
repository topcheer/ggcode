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
		t.Fatalf("expected anthropic params to marshal with fallback input, got %v", err)
	}
	if !strings.Contains(string(data), "_ggcode_raw_input") {
		t.Fatalf("expected fallback marker in marshaled params, got %s", string(data))
	}
}
