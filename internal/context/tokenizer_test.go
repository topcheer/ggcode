package context

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 1},
		{"ascii_short", "hello", 2},      // 5/4+1 = 2
		{"ascii_long", "hello world", 3}, // 11/4+1 = 3
		{"cjk_short", "你好", 2},           // 2*2/3+1 = 2
		{"cjk_long", "你好世界", 3},          // 4*2/3+1 = 3
		{"mixed", "hello你好", 3},          // 5/4+2*2/3+1 = 3
		{"single_ascii", "a", 1},         // 1/4+1 = 1
		{"single_cjk", "中", 1},           // 1*2/3+1 = 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.input)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEstimateTokens_CJKMoreThanASCII(t *testing.T) {
	ascii := EstimateTokens("abcdefgh")
	cjk := EstimateTokens("一二三四五六七八")
	if cjk <= ascii {
		t.Errorf("CJK tokens (%d) should be > ASCII tokens (%d)", cjk, ascii)
	}
}

func TestEstimateTokensWithMessageOverhead(t *testing.T) {
	// estimateTokens adds structural overhead per message.
	// A pure text message should be estimate + 4 (role overhead).
	pureText := "hello world this is a test"
	estimatedText := EstimateTokens(pureText)

	msg := provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: pureText},
		},
	}
	got := estimateTokens(msg)
	if got < estimatedText {
		t.Errorf("estimateTokens with overhead (%d) should be >= raw text estimate (%d)", got, estimatedText)
	}
	// Verify there's at least 4 tokens of overhead.
	if got < estimatedText+4 {
		t.Errorf("expected at least %d (text+4 overhead), got %d", estimatedText+4, got)
	}
}

func TestEstimateTokensWithToolCall(t *testing.T) {
	// Tool calls should add JSON structure overhead.
	msg := provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "read_file", Input: []byte(`{"path":"/foo.go"}`)},
		},
	}
	got := estimateTokens(msg)
	// Should include text estimate + 4 (message overhead) + 6 (tool call overhead).
	if got < 10 {
		t.Errorf("expected significant token estimate for tool call, got %d", got)
	}
}

func TestEstimateTokensWithImage(t *testing.T) {
	// Images should add ~170 tokens.
	msg := provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "image", ImageMIME: "image/png", ImageData: "dGVzdA=="},
		},
	}
	got := estimateTokens(msg)
	// Should include at least 170 (image) + 4 (message overhead).
	if got < 170 {
		t.Errorf("expected at least 174 tokens for image message, got %d", got)
	}
}

func TestEstimateTokensMultipleToolCalls(t *testing.T) {
	msg := provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "read_file", Input: []byte(`{"path":"/a.go"}`)},
			{Type: "tool_use", ToolName: "read_file", Input: []byte(`{"path":"/b.go"}`)},
			{Type: "tool_use", ToolName: "edit_file", Input: []byte(`{"path":"/c.go"}`)},
		},
	}
	got := estimateTokens(msg)
	// 3 tool calls × 6 overhead = 18 structural tokens.
	if got < 18 {
		t.Errorf("expected at least 18 tokens for 3 tool calls, got %d", got)
	}
}
