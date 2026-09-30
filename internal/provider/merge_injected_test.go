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

// TestFoldInjectedUserMessagesAppendOrder covers the append variant used by the
// Anthropic and Gemini legs (#2819): folded guidance must land AFTER the
// tool_result blocks so tool_result/functionResponse parts stay first in the
// user turn, as both APIs require.
func TestFoldInjectedUserMessagesAppendOrder(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: []ContentBlock{
			{Type: "tool_use", ToolID: "call_1", ToolName: "write_file"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "guidance warning"},
		}},
		{Role: "user", Content: []ContentBlock{
			{Type: "tool_result", ToolID: "call_1", Output: "file created"},
			{Type: "text", Text: "original text"},
		}},
	}

	result := foldInjectedUserMessages(msgs, appendToToolResultContent)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages after fold, got %d", len(result))
	}
	merged := result[1]
	if merged.Role != "user" {
		t.Fatalf("expected merged message to be user, got %q", merged.Role)
	}

	// The first block must remain a tool_result — APIs reject a text part first.
	if len(merged.Content) == 0 || merged.Content[0].Type != "tool_result" {
		t.Fatalf("expected first block of merged message to be tool_result, got %+v", merged.Content)
	}
	if merged.Content[0].ToolID != "call_1" {
		t.Errorf("expected tool_result ID 'call_1', got %q", merged.Content[0].ToolID)
	}

	// Guidance text must be present after the tool_result block.
	foundGuidance, foundOriginal := false, false
	for _, b := range merged.Content {
		if b.Type == "text" && strings.Contains(b.Text, "guidance warning") {
			foundGuidance = true
		}
		if b.Type == "text" && strings.Contains(b.Text, "original text") {
			foundOriginal = true
		}
	}
	if !foundGuidance {
		t.Errorf("expected folded guidance text in merged message, blocks: %+v", merged.Content)
	}
	if !foundOriginal {
		t.Errorf("expected original text block preserved, blocks: %+v", merged.Content)
	}
}

func TestAppendToToolResultContent(t *testing.T) {
	tests := []struct {
		name      string
		blocks    []ContentBlock
		prefix    string
		wantFirst string // expected type of the first block
		wantLast  string // expected substring of the last block's text
		wantLen   int
	}{
		{
			name: "inserts after trailing tool_result",
			blocks: []ContentBlock{
				{Type: "tool_result", ToolID: "c1", Output: "ok"},
				{Type: "tool_result", ToolID: "c2", Output: "ok2"},
			},
			prefix:    "warn\n\n",
			wantFirst: "tool_result",
			wantLast:  "warn",
			wantLen:   3,
		},
		{
			name: "inserts after tool_result but before trailing text",
			blocks: []ContentBlock{
				{Type: "tool_result", ToolID: "c1", Output: "ok"},
				{Type: "text", Text: "trailing"},
			},
			prefix:    "warn\n\n",
			wantFirst: "tool_result",
			wantLast:  "trailing",
			wantLen:   3,
		},
		{
			name:      "no tool_result appends at end",
			blocks:    []ContentBlock{{Type: "text", Text: "plain"}},
			prefix:    "warn\n\n",
			wantFirst: "text",
			wantLast:  "warn",
			wantLen:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := appendToToolResultContent(tt.blocks, tt.prefix)
			if len(result) != tt.wantLen {
				t.Fatalf("expected %d blocks, got %d: %+v", tt.wantLen, len(result), result)
			}
			if result[0].Type != tt.wantFirst {
				t.Errorf("expected first block %q, got %q", tt.wantFirst, result[0].Type)
			}
			if !strings.Contains(result[len(result)-1].Text, tt.wantLast) {
				t.Errorf("expected last block text to contain %q, got %+v", tt.wantLast, result[len(result)-1])
			}
		})
	}
}
