package agentruntime

// SEP-1577 (MCP 2025-11-25): tool-enabled sampling end-to-end through the
// agentruntime bridge - tools forwarded to the provider, tool_use response
// blocks relayed with stopReason "toolUse", tool_result messages mapped to
// provider tool-result blocks.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/provider"
)

type recordingSamplingProvider struct {
	provider.Provider
	chat     provider.ChatResponse
	gotTools []provider.ToolDefinition
	gotMsgs  []provider.Message
}

func (f *recordingSamplingProvider) Name() string { return "fake" }
func (f *recordingSamplingProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	f.gotTools = tools
	f.gotMsgs = msgs
	resp := f.chat
	return &resp, nil
}

func TestSEP1577_ToolsForwardedAndToolUseRelayed(t *testing.T) {
	p := &recordingSamplingProvider{chat: provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			provider.ToolUseBlock("call_1", "get_weather", json.RawMessage(`{"city":"Paris"}`)),
		}},
		StopReason: "toolUse",
	}}
	params := mcp.SamplingParams{
		Messages: []mcp.SamplingMessage{
			{Role: "user", Content: mcp.SamplingContent{Type: "text", Text: "weather?"}},
		},
		Tools: []mcp.SamplingTool{{
			Name:        "get_weather",
			Description: "Get current weather",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: &mcp.SamplingToolChoice{Mode: mcp.ToolChoiceAuto},
		MaxTokens:  100,
	}
	res, err := mcpSamplingHandlerWith(context.Background(), params, p)
	if err != nil {
		t.Fatal(err)
	}
	// Tools reach the provider in its native shape.
	if len(p.gotTools) != 1 || p.gotTools[0].Name != "get_weather" || string(p.gotTools[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("tools not forwarded: %+v", p.gotTools)
	}
	// tool_use blocks relayed as array content with stopReason toolUse.
	if res.StopReason != "toolUse" || len(res.Blocks) != 1 || res.Blocks[0].Type != "tool_use" || res.Blocks[0].ID != "call_1" {
		t.Fatalf("tool_use not relayed: %+v", res)
	}
	// maxTokens budget still honored through the shared-provider path.
	// (indirectly covered by the #1484/#1612 tests; here just sanity-check
	// the plain-text fallback path below.)
}

func TestSEP1577_ToolResultMessageMapped(t *testing.T) {
	p := &recordingSamplingProvider{chat: provider.ChatResponse{
		Message:    provider.Message{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("done")}},
		StopReason: "end_turn",
	}}
	params := mcp.SamplingParams{
		Messages: []mcp.SamplingMessage{
			{Role: "assistant", Blocks: []mcp.SamplingContent{{Type: "tool_use", ID: "c1", Name: "t", Input: json.RawMessage(`{}`)}}},
			{Role: "user", Blocks: []mcp.SamplingContent{{
				Type: "tool_result", ToolUseID: "c1", IsError: true,
				ResultContent: []mcp.SamplingContent{{Type: "text", Text: "boom"}},
			}}},
		},
		MaxTokens: 50,
	}
	res, err := mcpSamplingHandlerWith(context.Background(), params, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.gotMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(p.gotMsgs))
	}
	asst := p.gotMsgs[0]
	if asst.Role != "assistant" || len(asst.Content) != 1 || asst.Content[0].Type != "tool_use" || asst.Content[0].ToolID != "c1" {
		t.Fatalf("assistant tool_use not mapped: %+v", asst)
	}
	tres := p.gotMsgs[1]
	if tres.Role != "user" || len(tres.Content) != 1 || tres.Content[0].Type != "tool_result" || tres.Content[0].ToolID != "c1" || tres.Content[0].Output != "boom" || !tres.Content[0].IsError {
		t.Fatalf("tool_result not mapped: %+v", tres)
	}
	if res.StopReason != "end_turn" || res.Content.Text != "done" {
		t.Fatalf("plain result degraded: %+v", res)
	}
}

func TestSEP1577_StaleToolUseStopReasonWithoutBlocks(t *testing.T) {
	// Provider claims toolUse but emitted no tool_use blocks: must fall
	// back to end_turn instead of sending an unbalanced toolUse result.
	p := &recordingSamplingProvider{chat: textResp("plain", 10, "toolUse")}
	res, err := mcpSamplingHandlerWith(context.Background(), samplingParams(64), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("stale toolUse must fall back to end_turn, got %q", res.StopReason)
	}
}
