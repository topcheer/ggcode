package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestPreflushPayloads(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "you must obey"}}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "不要修改 tests"},
			{Type: "image", ImageData: "x"},
			{Type: "text", Text: "never push to main"},
		}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "run_command", Input: []byte(`{}`)},
			{Type: "tool_result", ToolID: "t1", Text: "exit code 0"},
		}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "普通句子"}}},
	}
	got := preflushPayloads(msgs)

	if len(got) != 2 {
		t.Fatalf("expected 2 payloads, got %d: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[1].Role != "user" {
		t.Fatalf("payload roles: %+v", got)
	}
	if !strings.Contains(got[0].Text, "不要修改 tests") || !strings.Contains(got[0].Text, "never push to main") {
		t.Fatalf("user text blocks must be joined with newline: %q", got[0].Text)
	}
	if got[1].Text != "普通句子\n" {
		t.Fatalf("plain user text: %q", got[1].Text)
	}
}
