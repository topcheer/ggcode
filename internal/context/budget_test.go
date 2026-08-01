package context

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestAnalyzeBudget_Empty(t *testing.T) {
	bd := AnalyzeBudget(nil)
	if bd == nil {
		t.Fatal("expected non-nil breakdown")
	}
	if bd.TotalTokens != 0 {
		t.Errorf("expected 0 total tokens, got %d", bd.TotalTokens)
	}
	if len(bd.Categories) != 0 {
		t.Errorf("expected 0 categories, got %d", len(bd.Categories))
	}
}

func TestAnalyzeBudget_BasicCategorization(t *testing.T) {
	msgs := []provider.Message{
		{
			Role: "system",
			Content: []provider.ContentBlock{
				provider.TextBlock("You are a helpful assistant."),
			},
		},
		{
			Role: "user",
			Content: []provider.ContentBlock{
				provider.TextBlock("Please help me write some code."),
			},
		},
		{
			Role: "assistant",
			Content: []provider.ContentBlock{
				provider.TextBlock("I'll help you with that."),
				provider.ToolUseBlock("call_1", "read_file", json.RawMessage(`{"path":"foo.go"}`)),
			},
		},
		{
			Role: "user",
			Content: []provider.ContentBlock{
				{
					Type:   "tool_result",
					ToolID: "call_1",
					Output: "package main\nfunc main() {}\n",
				},
			},
		},
	}

	bd := AnalyzeBudget(msgs)
	if bd == nil {
		t.Fatal("expected non-nil breakdown")
	}

	// Verify total > 0
	if bd.TotalTokens == 0 {
		t.Error("expected non-zero total tokens")
	}

	// Find categories
	catMap := make(map[BudgetCategory]CategoryTokens)
	for _, c := range bd.Categories {
		catMap[c.Category] = c
	}

	// System category should exist
	if _, ok := catMap[CategorySystem]; !ok {
		t.Error("expected system category")
	}

	// User category should exist
	if _, ok := catMap[CategoryUser]; !ok {
		t.Error("expected user category")
	}

	// Tool call category should exist
	if tc, ok := catMap[CategoryToolCall]; ok {
		if tc.Count == 0 {
			t.Error("expected at least 1 tool call")
		}
	} else {
		t.Error("expected tool_call category")
	}

	// Tool result category should exist
	if tr, ok := catMap[CategoryToolResult]; ok {
		if tr.Count == 0 {
			t.Error("expected at least 1 tool result")
		}
	} else {
		t.Error("expected tool_result category")
	}
}

func TestAnalyzeBudget_TopMessages(t *testing.T) {
	shortText := "short"
	longText := strings.Repeat("This is a very long message that consumes many tokens. ", 50)

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(shortText)}},
		{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock(longText)}},
	}

	bd := AnalyzeBudget(msgs)
	if len(bd.TopMessages) == 0 {
		t.Fatal("expected top messages")
	}

	// The long message should be first
	if bd.TopMessages[0].Tokens < bd.TopMessages[len(bd.TopMessages)-1].Tokens {
		t.Error("expected top messages sorted by tokens descending")
	}

	// Verify the long message has more tokens
	if bd.TopMessages[0].Tokens <= 10 {
		t.Errorf("expected largest message to have many tokens, got %d", bd.TopMessages[0].Tokens)
	}
}

func TestAnalyzeBudget_LargestToolResults(t *testing.T) {
	largeResult := strings.Repeat("data line\n", 100)

	msgs := []provider.Message{
		{
			Role: "user",
			Content: []provider.ContentBlock{
				{
					Type:   "tool_result",
					ToolID: "call_1",
					Output: largeResult,
				},
			},
		},
		{
			Role: "user",
			Content: []provider.ContentBlock{
				{
					Type:   "tool_result",
					ToolID: "call_2",
					Output: "small result",
				},
			},
		},
	}

	bd := AnalyzeBudget(msgs)
	if len(bd.LargestToolResults) == 0 {
		t.Fatal("expected largest tool results")
	}

	// The large result should be first
	if bd.LargestToolResults[0].Tokens < 100 {
		t.Errorf("expected largest tool result to have many tokens, got %d",
			bd.LargestToolResults[0].Tokens)
	}
}

func TestAnalyzeBudget_Percentage(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{provider.TextBlock("system prompt")}},
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("user message")}},
	}

	bd := AnalyzeBudget(msgs)

	// Percentages should sum to ~100%
	sysPct := bd.Percentage(CategorySystem)
	userPct := bd.Percentage(CategoryUser)

	if sysPct <= 0 {
		t.Error("expected system percentage > 0")
	}
	if userPct <= 0 {
		t.Error("expected user percentage > 0")
	}
	if sysPct+userPct < 99 || sysPct+userPct > 101 {
		t.Errorf("expected percentages to sum to ~100%%, got %.1f%%", sysPct+userPct)
	}
}

func TestAnalyzeBudget_TokenEstimation(t *testing.T) {
	// ~4 chars per token
	text := "abcdefgh" // 8 chars = 2 tokens
	tokens := estimateTokensChars(text)
	if tokens != 2 {
		t.Errorf("expected 2 tokens for 8 chars, got %d", tokens)
	}

	text = "abc" // 3 chars = 1 token (rounded up)
	tokens = estimateTokensChars(text)
	if tokens != 1 {
		t.Errorf("expected 1 token for 3 chars, got %d", tokens)
	}

	text = ""
	tokens = estimateTokensChars(text)
	if tokens != 0 {
		t.Errorf("expected 0 tokens for empty string, got %d", tokens)
	}
}

func TestBudgetBreakdown_FormatHumanReadable(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{provider.TextBlock("system prompt")}},
		{
			Role: "user",
			Content: []provider.ContentBlock{
				{
					Type:   "tool_result",
					ToolID: "call_1",
					Output: "some result data",
				},
			},
		},
	}

	bd := AnalyzeBudget(msgs)
	output := bd.FormatHumanReadable()

	if !strings.Contains(output, "Context Budget") {
		t.Error("expected output to contain 'Context Budget'")
	}
	if !strings.Contains(output, "system") {
		t.Error("expected output to contain 'system' category")
	}
	if !strings.Contains(output, "tool_result") {
		t.Error("expected output to contain 'tool_result' category")
	}
}

func TestAnalyzeBudget_ErrorToolResult(t *testing.T) {
	msgs := []provider.Message{
		{
			Role: "user",
			Content: []provider.ContentBlock{
				{
					Type:    "tool_result",
					ToolID:  "call_1",
					Output:  "Error: file not found",
					IsError: true,
				},
			},
		},
	}

	bd := AnalyzeBudget(msgs)
	if len(bd.TopMessages) == 0 {
		t.Fatal("expected top messages")
	}

	// Preview should indicate error
	if !strings.Contains(bd.TopMessages[0].Preview, "error") {
		t.Errorf("expected preview to indicate error, got: %s", bd.TopMessages[0].Preview)
	}
}

func TestAnalyzeBudget_ImageBlock(t *testing.T) {
	msgs := []provider.Message{
		{
			Role: "user",
			Content: []provider.ContentBlock{
				provider.ImageBlock("image/png", "base64data"),
			},
		},
	}

	bd := AnalyzeBudget(msgs)
	if bd.TotalTokens < 300 {
		t.Errorf("expected image to contribute ~300 tokens, got %d", bd.TotalTokens)
	}
}

func TestMessagePreview_Truncation(t *testing.T) {
	longMsg := provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			provider.TextBlock(strings.Repeat("x", 200)),
		},
	}

	preview := messagePreview(longMsg)
	if len(preview) > 83 { // 80 + "..."
		t.Errorf("preview too long: %d chars", len(preview))
	}
	if !strings.HasSuffix(preview, "...") {
		t.Error("expected truncated preview to end with ...")
	}
}
