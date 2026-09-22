package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tests for the conversation-tail cache breakpoint and the 4-breakpoint
// budget allocator in buildParams ("Don't Break the Cache",
// arXiv:2601.06007; Anthropic agentic caching pattern).

func cacheAgentMessages() []Message {
	return []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "static base", Cache: true}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "find the bug"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolID: "tu_1", ToolName: "grep", Input: json.RawMessage(`{"pattern":"x"}`)}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolID: "tu_1", Output: "no matches"}}},
	}
}

func cacheTestTools() []ToolDefinition {
	return []ToolDefinition{
		{Name: "grep", Description: "search", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
	}
}

func countTailBreakpoints(t *testing.T, params anthropic.MessageNewParams) int {
	t.Helper()
	n := 0
	for _, m := range params.Messages {
		for _, b := range m.Content {
			if b.OfToolResult != nil && b.OfToolResult.CacheControl.Type != "" {
				n++
			}
			if b.OfText != nil && b.OfText.CacheControl.Type != "" {
				n++
			}
		}
	}
	for _, b := range params.System {
		if b.CacheControl.Type != "" {
			n++
		}
	}
	return n
}

func TestTailCacheBreakpointOnToolResult(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}
	params := p.buildParams(context.Background(), cacheAgentMessages(), cacheTestTools())

	if len(params.Messages) == 0 {
		t.Fatal("expected messages")
	}
	last := params.Messages[len(params.Messages)-1]
	if len(last.Content) == 0 {
		t.Fatal("expected content in last message")
	}
	lb := last.Content[len(last.Content)-1]
	if lb.OfToolResult == nil {
		t.Fatalf("expected tool_result as last block, got %#v", lb)
	}
	if lb.OfToolResult.CacheControl.Type == "" {
		t.Fatal("expected cache breakpoint on the trailing tool_result block")
	}

	// System prefix and tool schema breakpoints must survive (3 total <= 4).
	// System blocks are flushed into the first user message when one exists.
	first := params.Messages[0]
	sysFound := false
	for _, b := range first.Content {
		if b.OfText != nil && strings.Contains(b.OfText.Text, "static base") {
			sysFound = b.OfText.CacheControl.Type != ""
		}
	}
	if !sysFound {
		t.Fatal("expected cache breakpoint on the hinted system block")
	}
	toolBPs := 0
	for _, u := range params.Tools {
		if u.OfTool != nil && u.OfTool.CacheControl.Type != "" {
			toolBPs++
		}
	}
	if toolBPs != 1 {
		t.Fatalf("expected exactly 1 tool schema breakpoint, got %d", toolBPs)
	}
	if total := countTailBreakpoints(t, params); total > 4 {
		t.Fatalf("breakpoint budget exceeded: %d > 4", total)
	}
}

func TestTailCacheBreakpointSkipsAssistantPrefill(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}
	msgs := cacheAgentMessages()
	msgs = append(msgs, Message{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "prefill"}}})
	params := p.buildParams(context.Background(), msgs, cacheTestTools())

	last := params.Messages[len(params.Messages)-1]
	for _, b := range last.Content {
		if b.OfText != nil && b.OfText.CacheControl.Type != "" {
			t.Fatal("assistant prefill must not receive a cache breakpoint")
		}
	}
}

func TestTailBreakpointWithServerToolsDropsRedundantToolBP(t *testing.T) {
	p := &AnthropicProvider{
		model:       "claude-sonnet-4-6",
		maxTokens:   64000,
		serverTools: []ServerToolConfig{{Type: "web_search_20250305"}},
	}
	params := p.buildParams(context.Background(), cacheAgentMessages(), cacheTestTools())

	// Server tools are appended after the regular tools; their breakpoint
	// prefix-covers the regular ones, so the redundant regular-tools
	// breakpoint is skipped to save budget.
	toolBPs := 0
	serverBPs := 0
	for _, u := range params.Tools {
		switch {
		case u.OfTool != nil && u.OfTool.CacheControl.Type != "":
			toolBPs++
		case u.OfWebSearchTool20250305 != nil && u.OfWebSearchTool20250305.CacheControl.Type != "":
			serverBPs++
		}
	}
	if toolBPs != 0 {
		t.Fatalf("expected regular tool breakpoint to be skipped when server tools follow, got %d", toolBPs)
	}
	if serverBPs != 1 {
		t.Fatalf("expected exactly 1 server tool breakpoint, got %d", serverBPs)
	}

	// The conversation-tail breakpoint must still fit within the budget.
	last := params.Messages[len(params.Messages)-1]
	lb := last.Content[len(last.Content)-1]
	if lb.OfToolResult == nil || lb.OfToolResult.CacheControl.Type == "" {
		t.Fatal("expected tail breakpoint on the trailing tool_result even with server tools")
	}
	if total := countTailBreakpoints(t, params); total > 4 {
		t.Fatalf("breakpoint budget exceeded: %d > 4", total)
	}
}

func TestSystemBreakpointBudgetCap(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{
			{Type: "text", Text: "sys1", Cache: true},
			{Type: "text", Text: "sys2", Cache: true},
			{Type: "text", Text: "sys3", Cache: true},
		}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	params := p.buildParams(context.Background(), msgs, cacheTestTools())

	// Budget: 4 - tools(1) - tail(1) = 2 system breakpoints. System blocks
	// are flushed into the first user message when one exists; count only
	// the system blocks (the trailing "hi" text carries the tail breakpoint).
	sysBPs := 0
	for _, b := range params.Messages[0].Content {
		if b.OfText != nil && (b.OfText.Text == "sys1" || b.OfText.Text == "sys2" ||
			strings.Contains(b.OfText.Text, "sys1") || strings.Contains(b.OfText.Text, "sys3")) {
			if b.OfText.CacheControl.Type != "" {
				sysBPs++
			}
		}
	}
	if sysBPs != 2 {
		t.Fatalf("expected system breakpoints capped at 2, got %d", sysBPs)
	}

	// Tail breakpoint must still be applied to the trailing user text.
	last := params.Messages[len(params.Messages)-1]
	lb := last.Content[len(last.Content)-1]
	if lb.OfText == nil || lb.OfText.CacheControl.Type == "" {
		t.Fatal("expected tail breakpoint on the trailing user text block")
	}
}

func TestTailBreakpointWalksPastUnsupportedBlock(t *testing.T) {
	p := &AnthropicProvider{model: "claude-sonnet-4-6", maxTokens: 64000}
	msgs := cacheAgentMessages()
	// thinking echoes cannot carry cache_control; the tail breakpoint must
	// walk over them to the tool_result block in the same message.
	msgs[len(msgs)-1].Content = append([]ContentBlock{
		{Type: "thinking", ThinkingSignature: "sig", ReasoningContent: "thought"},
	}, msgs[len(msgs)-1].Content...)
	params := p.buildParams(context.Background(), msgs, cacheTestTools())

	last := params.Messages[len(params.Messages)-1]
	found := false
	for _, b := range last.Content {
		if b.OfToolResult != nil && b.OfToolResult.CacheControl.Type != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected tail breakpoint to land on the tool_result after walking past the thinking block")
	}
}

func TestSetMsgBlockCacheControl(t *testing.T) {
	text := anthropic.NewTextBlock("x")
	if got := setMsgBlockCacheControl(&text); got != 2 {
		t.Fatalf("expected 2 (applied) for text block, got %d", got)
	}
	if got := setMsgBlockCacheControl(&text); got != 1 {
		t.Fatalf("expected 1 (already set) for text block, got %d", got)
	}
	tr := anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{ToolUseID: "t1"}}
	if got := setMsgBlockCacheControl(&tr); got != 2 {
		t.Fatalf("expected 2 (applied) for tool_result block, got %d", got)
	}
	thinking := anthropic.NewThinkingBlock("sig", "thought")
	if got := setMsgBlockCacheControl(&thinking); got != 0 {
		t.Fatalf("expected 0 (unsupported) for thinking block, got %d", got)
	}
}

func TestHasCacheableServerTool(t *testing.T) {
	if hasCacheableServerTool(nil) {
		t.Fatal("nil server tools must report false")
	}
	if hasCacheableServerTool([]ServerToolConfig{{Type: "unknown_tool"}}) {
		t.Fatal("unknown types must report false")
	}
	if !hasCacheableServerTool([]ServerToolConfig{{Type: "web_fetch_20250910"}}) {
		t.Fatal("known type must report true")
	}
}
