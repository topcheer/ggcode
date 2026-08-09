package provider

import (
	"strings"
	"testing"
)

func TestMergeInjectedUserMessages(t *testing.T) {
	tests := []struct {
		name                     string
		messages                 []Message
		wantText                 string // text that should appear in the merged result
		wantNoStandaloneGuidance bool
	}{
		{
			name: "text-only user between assistant tool_use and tool_result gets merged",
			messages: []Message{
				{Role: "assistant", Content: []ContentBlock{
					{Type: "tool_use", ToolID: "call_1", ToolName: "write_file"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "text", Text: "guidance warning"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "tool_result", ToolID: "call_1", Output: "file created"},
				}},
			},
			wantText:                 "guidance warning",
			wantNoStandaloneGuidance: true,
		},
		{
			name: "multiple text-only user messages merged",
			messages: []Message{
				{Role: "assistant", Content: []ContentBlock{
					{Type: "tool_use", ToolID: "call_1", ToolName: "edit_file"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "text", Text: "warning 1"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "text", Text: "warning 2"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "tool_result", ToolID: "call_1", Output: "ok"},
				}},
			},
			wantText:                 "warning 1",
			wantNoStandaloneGuidance: true,
		},
		{
			name: "no injection - normal flow preserved",
			messages: []Message{
				{Role: "assistant", Content: []ContentBlock{
					{Type: "tool_use", ToolID: "call_1", ToolName: "read_file"},
				}},
				{Role: "user", Content: []ContentBlock{
					{Type: "tool_result", ToolID: "call_1", Output: "content"},
				}},
			},
			wantText:                 "content",
			wantNoStandaloneGuidance: false,
		},
		{
			name: "too few messages - no change",
			messages: []Message{
				{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
				{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
			},
			wantText:                 "hello",
			wantNoStandaloneGuidance: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeInjectedUserMessages(tt.messages)

			// Check the guidance text is present somewhere in the result
			found := false
			standaloneGuidance := false
			for _, m := range result {
				for _, b := range m.Content {
					// Check all text fields including Output
					textToCheck := b.Text
					if b.Type == "tool_result" {
						textToCheck = b.Output
					}
					if strings.Contains(textToCheck, tt.wantText) {
						found = true
					}
					// Check if guidance is a standalone text-only user message
					if b.Type == "text" && strings.Contains(b.Text, tt.wantText) {
						if m.Role == "user" && isTextOnly(m.Content) {
							standaloneGuidance = true
						}
					}
				}
			}

			if !found && tt.wantText != "" {
				t.Errorf("expected text %q in result, not found", tt.wantText)
			}
			if tt.wantNoStandaloneGuidance && standaloneGuidance {
				t.Errorf("guidance text should be merged into tool_result, not standalone")
			}
		})
	}
}

func TestMergeInjectedUserMessagesPreservesIDs(t *testing.T) {
	// Ensure tool_call IDs are preserved after merging
	msgs := []Message{
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ToolID: "write_file:13", ToolName: "write_file"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "hedging detector warning"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "write_file:13", Output: "created"},
		}},
	}

	result := mergeInjectedUserMessages(msgs)

	// Should have exactly 2 messages: assistant + merged tool_result
	if len(result) != 2 {
		t.Fatalf("expected 2 messages after merge, got %d", len(result))
	}

	// Tool result should preserve the ID
	var toolResultID string
	for _, m := range result {
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				toolResultID = b.ToolID
			}
		}
	}
	if toolResultID != "write_file:13" {
		t.Errorf("expected tool_result ID 'write_file:13', got %q", toolResultID)
	}
}
