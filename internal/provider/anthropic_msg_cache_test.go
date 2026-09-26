package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func msgParamsFromMessages(t *testing.T, p *AnthropicProvider, msgs []Message) anthropic.MessageNewParams {
	t.Helper()
	return p.buildParams(t.Context(), msgs, []ToolDefinition{
		{Name: "read_file", Description: "read", Parameters: json.RawMessage(`{"type":"object"}`)},
	})
}

func countMessageBreakpoints(params anthropic.MessageNewParams) int {
	n := 0
	for _, m := range params.Messages {
		for _, blk := range m.Content {
			switch {
			case blk.OfToolResult != nil && blk.OfToolResult.CacheControl != (anthropic.CacheControlEphemeralParam{}):
				n++
			case blk.OfText != nil && blk.OfText.CacheControl != (anthropic.CacheControlEphemeralParam{}):
				n++
			case blk.OfToolUse != nil && blk.OfToolUse.CacheControl != (anthropic.CacheControlEphemeralParam{}):
				n++
			case blk.OfImage != nil && blk.OfImage.CacheControl != (anthropic.CacheControlEphemeralParam{}):
				n++
			}
		}
	}
	return n
}

// Default behavior: exactly one breakpoint lands on the conversation tail
// (the latest tool_result), on top of the tool + system breakpoints.
func TestIncrementalCacheBreakpointOnToolResult(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "list files"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ToolID: "t1", ToolName: "list_directory"}}},
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolID: "t1", Output: "a.go\nb.go"}}},
	}
	params := msgParamsFromMessages(t, p, msgs)
	if got := countMessageBreakpoints(params); got != 1 {
		t.Fatalf("expected exactly 1 message breakpoint, got %d", got)
	}
	last := params.Messages[len(params.Messages)-1]
	if last.Content[0].OfToolResult == nil || last.Content[0].OfToolResult.CacheControl == (anthropic.CacheControlEphemeralParam{}) {
		t.Fatalf("breakpoint expected on latest tool_result, got %+v", last.Content[0])
	}
}

// Breakpoint budget: tools (1) + system (1) already used; with 3 more
// conversation-rounds simulated by injected breakpoints the message layer
// must stay silent once the 4-breakpoint cap is reached.
func TestIncrementalCacheBreakpointBudget(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolID: "t1", Output: "ok"}}},
	}
	// Simulate a full budget: system (cache hint) + tools + 2 extra markers
	// via two synthetic cache-eligible system blocks is still only 2, so
	// instead force the count by checking the helper directly.
	params := msgParamsFromMessages(t, p, msgs)
	params.System = append(params.System,
		anthropic.TextBlockParam{Text: "a", CacheControl: anthropic.NewCacheControlEphemeralParam()},
		anthropic.TextBlockParam{Text: "b", CacheControl: anthropic.NewCacheControlEphemeralParam()},
	)
	// 1 tool + 2 system = 3 used; one slot remains.
	if used := countCacheBreakpoints(&params); used != 3 {
		t.Fatalf("expected counted budget 3, got %d", used)
	}
	if !applyIncrementalCacheBreakpoint(params.Messages) {
		t.Fatalf("expected breakpoint within remaining budget")
	}
	// Fill to cap; further application must be refused by addMessageCacheBreakpoint.
	params.System = append(params.System, anthropic.TextBlockParam{Text: "c", CacheControl: anthropic.NewCacheControlEphemeralParam()})
	if used := countCacheBreakpoints(&params); used != anthropicMaxCacheBreakpoints {
		t.Fatalf("expected budget exhausted (4), got %d", used)
	}
	addMessageCacheBreakpoint(&params)
	if got := countMessageBreakpoints(params); got != 1 {
		t.Fatalf("at budget cap no additional message breakpoint expected, got %d", got)
	}
}

// Thinking blocks cannot carry cache_control; the marker must land on the
// nearest preceding eligible block (here, the tool_use).
func TestIncrementalCacheBreakpointSkipsThinking(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	msgs := []Message{
		{Role: "assistant", Content: []ContentBlock{
			{Type: "thinking", ReasoningContent: "hmm", ThinkingSignature: "sig"},
			{Type: "tool_use", ToolID: "t2", ToolName: "read_file"},
		}},
	}
	params := msgParamsFromMessages(t, p, msgs)
	blocks := params.Messages[len(params.Messages)-1].Content
	if len(blocks) != 2 {
		t.Fatalf("expected 2 echoed blocks, got %d", len(blocks))
	}
	// ThinkingBlockParam has no CacheControl field at all (API rejects
	// cache_control on thinking); the marker must land on the tool_use.
	if blocks[1].OfToolUse == nil || blocks[1].OfToolUse.CacheControl == (anthropic.CacheControlEphemeralParam{}) {
		t.Fatalf("expected breakpoint on tool_use tail block, got %+v", blocks[1])
	}
	if blocks[0].OfThinking == nil {
		t.Fatalf("expected thinking first")
	}
}

// Kill switch restores the old behavior.
func TestIncrementalCacheBreakpointKillSwitch(t *testing.T) {
	t.Setenv("GGCODE_MSG_CACHE_BREAKPOINT", "off")
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolID: "t1", Output: "ok"}}},
	}
	params := msgParamsFromMessages(t, p, msgs)
	if got := countMessageBreakpoints(params); got != 0 {
		t.Fatalf("kill switch active: expected 0 message breakpoints, got %d", got)
	}
}

// Wire-level sanity: the breakpoint serializes as cache_control on the tail
// tool_result of the request JSON.
func TestIncrementalCacheBreakpointWire(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolID: "t9", Output: "done"}}},
	}
	params := msgParamsFromMessages(t, p, msgs)
	raw, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"cache_control"`) {
		t.Fatalf("expected cache_control on wire, got %s", raw)
	}
}
