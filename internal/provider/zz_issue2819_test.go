package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// zz_issue2819_test.go - probe for #2819: mid-loop detectors inject guidance as
// text-only user messages between assistant tool_use and user tool_result.
// Anthropic rejects that sequence with a 400 ("tool_use ids found without
// tool_result blocks immediately after"), so buildParams (and
// buildCountTokensParams) must fold those messages into the tool_result turn,
// keeping tool_result blocks FIRST in the user message content.
func TestIssue2819AnthropicFoldsInjectedUserMessages(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "edit_file", Input: json.RawMessage(`{"file_path":"a.go"}`)},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "[detector guidance] warning text"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "t1", Output: "ok"},
		}},
	}

	p := &AnthropicProvider{}
	params := p.buildParams(context.Background(), msgs, nil)

	if len(params.Messages) != 2 {
		t.Fatalf("#2819: expected injected message folded (2 messages), got %d", len(params.Messages))
	}
	if string(params.Messages[0].Role) != "assistant" || string(params.Messages[1].Role) != "user" {
		t.Fatalf("#2819: unexpected roles: %v, %v", params.Messages[0].Role, params.Messages[1].Role)
	}
	content := params.Messages[1].Content
	if len(content) == 0 || content[0].OfToolResult == nil {
		t.Fatalf("#2819: first block of tool_result turn must be a tool_result block (Anthropic ordering), got %+v", content)
	}
	if content[0].OfToolResult.ToolUseID != "t1" {
		t.Fatalf("#2819: tool_result block id = %q, want t1", content[0].OfToolResult.ToolUseID)
	}
	found := false
	for _, b := range content[1:] {
		if b.OfText != nil && strings.Contains(b.OfText.Text, "[detector guidance]") {
			found = true
		}
	}
	if !found {
		t.Fatalf("#2819: folded guidance text not found after tool_result block: %+v", content)
	}

	// Same invariant for the countTokens path.
	ctParams := p.buildCountTokensParams(msgs)
	if len(ctParams.Messages) != 2 {
		t.Fatalf("#2819: countTokens path: expected 2 messages, got %d", len(ctParams.Messages))
	}
	ctContent := ctParams.Messages[1].Content
	if len(ctContent) == 0 || ctContent[0].OfToolResult == nil {
		t.Fatalf("#2819: countTokens path: first block must be tool_result, got %+v", ctContent)
	}
}
