package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// TestServerToolBlockEchoBackRoundTrip verifies the verbatim capture →
// request-side param conversion round trip that keeps the API contract:
// the server_tool_use + result pair must be echoed back losslessly or the
// API treats the call as deferred and re-runs the tool.
func TestServerToolBlockEchoBackRoundTrip(t *testing.T) {
	useJSON := `{"type":"server_tool_use","id":"srvtoolu_01ABC","name":"web_search","input":{"query":"ggcode anthropic"}}`
	resultJSON := `{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01ABC","content":[{"type":"web_search_result","title":"Anthropic","url":"https://example.com","encrypted_index":"EI"}]}`

	var useBlock, resultBlock anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(useJSON), &useBlock); err != nil {
		t.Fatalf("unmarshal server_tool_use: %v", err)
	}
	if err := json.Unmarshal([]byte(resultJSON), &resultBlock); err != nil {
		t.Fatalf("unmarshal web_search_tool_result: %v", err)
	}

	// 1. Non-streaming conversion keeps server blocks verbatim.
	converted := convertAnthropicResponse([]anthropic.ContentBlockUnion{useBlock, resultBlock})
	if len(converted) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(converted))
	}
	for i, want := range []string{"server_tool_use", "web_search_tool_result"} {
		if converted[i].Type != want {
			t.Fatalf("block %d type = %q, want %q", i, converted[i].Type, want)
		}
		if len(converted[i].Raw) == 0 {
			t.Fatalf("block %d missing Raw payload", i)
		}
	}
	if !strings.Contains(string(converted[0].Raw), `"query":"ggcode anthropic"`) {
		t.Fatalf("server_tool_use Raw not verbatim: %s", converted[0].Raw)
	}

	// 2. Raw converts back to request-side params.
	useParam, err := serverToolBlockParam(converted[0].Raw)
	if err != nil {
		t.Fatalf("serverToolBlockParam(use): %v", err)
	}
	if useParam.OfServerToolUse == nil || useParam.OfServerToolUse.ID != "srvtoolu_01ABC" || useParam.OfServerToolUse.Name != "web_search" {
		t.Fatalf("use param mismatch: %+v", useParam)
	}
	// ToParam() decodes Input into map[string]any; compare semantically.
	useInput, err := json.Marshal(useParam.OfServerToolUse.Input)
	if err != nil || string(useInput) != `{"query":"ggcode anthropic"}` {
		t.Fatalf("use param input not preserved: %v (%v)", useParam.OfServerToolUse.Input, err)
	}

	resultParam, err := serverToolBlockParam(converted[1].Raw)
	if err != nil {
		t.Fatalf("serverToolBlockParam(result): %v", err)
	}
	if resultParam.OfWebSearchToolResult == nil || resultParam.OfWebSearchToolResult.ToolUseID != "srvtoolu_01ABC" {
		t.Fatalf("result param mismatch: %+v", resultParam)
	}

	// 3. Round-trip fidelity: params marshal back to equivalent JSON.
	useBack, _ := json.Marshal(useParam)
	var useRound anthropic.ServerToolUseBlock
	if err := json.Unmarshal(useBack, &useRound); err != nil {
		t.Fatalf("re-unmarshal use param: %v", err)
	}
	if useRound.ID != "srvtoolu_01ABC" || useRound.Name != "web_search" {
		t.Fatalf("use round-trip mismatch: %+v", useRound)
	}
}

// TestServerToolDeclarationInParams verifies web_search/web_fetch are declared
// as Anthropic-native tool unions with a cache breakpoint on the last one.
func TestServerToolDeclarationInParams(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	p.SetServerTools([]ServerToolConfig{
		{Type: "web_search_20250305"},
		{Type: "web_fetch_20250910"},
	})

	params := p.buildParams(context.Background(), []Message{}, nil)
	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 tool unions, got %d", len(params.Tools))
	}
	first, second := params.Tools[0], params.Tools[1]
	if first.OfWebSearchTool20250305 == nil {
		t.Fatalf("first union is not web_search_20250305: %+v", first)
	}
	if second.OfWebFetchTool20250910 == nil {
		t.Fatalf("second union is not web_fetch_20250910: %+v", second)
	}
	if second.OfWebFetchTool20250910.CacheControl.Type == "" {
		t.Fatal("cache breakpoint missing on last server tool declaration")
	}

	// Wire format sanity: serialized declaration carries the native types.
	raw, err := json.Marshal(params.Tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	for _, want := range []string{"web_search_20250305", "web_fetch_20250910"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("serialized tools missing %q: %s", want, raw)
		}
	}
}

// TestUnknownServerToolTypeIgnored ensures an unsupported declaration is
// skipped rather than producing an invalid request payload.
func TestUnknownServerToolTypeIgnored(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	p.SetServerTools([]ServerToolConfig{{Type: "definitely_not_real_20260101"}})

	params := p.buildParams(context.Background(), []Message{}, nil)
	if len(params.Tools) != 0 {
		t.Fatalf("unknown server tool type must be ignored, got %d tools", len(params.Tools))
	}
}

// TestServerToolBlockParamRejectsUnknown ensures echo conversion fails closed.
func TestServerToolBlockParamRejectsUnknown(t *testing.T) {
	if _, err := serverToolBlockParam(json.RawMessage(`{"type":"mystery_block"}`)); err == nil {
		t.Fatal("expected error for unknown block type")
	}
	if _, err := serverToolBlockParam(json.RawMessage(`not-json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestToolSearchDeclarationAndBetaOpts verifies the server-side Tool Search
// Tool declarations (tool_search_tool_regex/bm25, advanced-tool-use beta):
// native union types, cache breakpoints, active-flag, and beta request opts.
func TestToolSearchDeclarationAndBetaOpts(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	if p.ServerToolSearchActive() {
		t.Fatal("tool search must be inactive before SetServerTools")
	}
	p.SetServerTools([]ServerToolConfig{
		{Type: "tool_search_tool_regex"},
		{Type: "tool_search_tool_bm25"},
	})
	if !p.ServerToolSearchActive() {
		t.Fatal("ServerToolSearchActive must be true with tool_search_tool_* configured")
	}
	opts := p.serverToolOpts()
	if len(opts) != 1 {
		t.Fatalf("expected 1 beta request opt, got %d", len(opts))
	}
	// Inactive providers must not send the header (proxy compatibility).
	q := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	q.SetServerTools([]ServerToolConfig{{Type: "web_search_20250305"}})
	if len(q.serverToolOpts()) != 0 {
		t.Fatal("beta header must be omitted without tool search")
	}

	params := p.buildParams(context.Background(), []Message{}, nil)
	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 tool unions, got %d", len(params.Tools))
	}
	if params.Tools[0].OfToolSearchToolRegex20251119 == nil {
		t.Fatalf("first union is not tool_search_tool_regex: %+v", params.Tools[0])
	}
	if params.Tools[1].OfToolSearchToolBm25_20251119 == nil {
		t.Fatalf("second union is not tool_search_tool_bm25: %+v", params.Tools[1])
	}
	raw, err := json.Marshal(params.Tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	for _, want := range []string{"tool_search_tool_regex_20251119", "tool_search_tool_bm25_20251119"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("serialized tools missing %q: %s", want, raw)
		}
	}
}

// TestToolSearchResultEchoRoundTrip verifies tool_search_tool_result blocks
// survive capture → conversion → param echo-back so tool_reference expansions
// the API produced are not treated as deferred on the next turn.
func TestToolSearchResultEchoRoundTrip(t *testing.T) {
	rawJSON := `{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_01TS","content":{"type":"tool_search_tool_search_result","tool_references":[{"type":"tool_reference","tool_name":"mcp__srv__tool_01"}]}}`
	var block anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(rawJSON), &block); err != nil {
		t.Fatalf("unmarshal tool_search_tool_result: %v", err)
	}
	converted := convertAnthropicResponse([]anthropic.ContentBlockUnion{block})
	if len(converted) != 1 || converted[0].Type != "tool_search_tool_result" {
		t.Fatalf("conversion lost tool_search_tool_result: %+v", converted)
	}
	if !strings.Contains(string(converted[0].Raw), `"tool_name":"mcp__srv__tool_01"`) {
		t.Fatalf("raw payload not verbatim: %s", converted[0].Raw)
	}
	param, err := serverToolBlockParam(converted[0].Raw)
	if err != nil {
		t.Fatalf("serverToolBlockParam: %v", err)
	}
	if param.OfToolSearchToolResult == nil {
		t.Fatalf("param is not OfToolSearchToolResult: %+v", param)
	}
	if param.OfToolSearchToolResult.ToolUseID != "srvtoolu_01TS" {
		t.Fatalf("tool_use_id mismatch: %q", param.OfToolSearchToolResult.ToolUseID)
	}
	back, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal param: %v", err)
	}
	if !strings.Contains(string(back), `"tool_search_tool_result"`) || !strings.Contains(string(back), `srvtoolu_01TS`) {
		t.Fatalf("echo param does not round-trip: %s", back)
	}
}

// TestDeferLoadingToolParams verifies defer_loading serialization and the
// cache-breakpoint guard: the API rejects defer_loading combined with a
// cache_control breakpoint on the same definition, so the breakpoint moves
// to the last non-deferred tool.
func TestDeferLoadingToolParams(t *testing.T) {
	p := NewAnthropicProviderWithBaseURL("test-key", "claude-sonnet-4-5", 1024, "https://api.anthropic.com")
	tools := []ToolDefinition{
		{Name: "mcp__srv__a", Description: "a", Parameters: json.RawMessage(`{"type":"object"}`), DeferLoading: true},
		{Name: "mcp__srv__b", Description: "b", Parameters: json.RawMessage(`{"type":"object"}`), DeferLoading: true},
		{Name: "builtin_read", Description: "r", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	params := p.buildParams(context.Background(), []Message{}, tools)
	if len(params.Tools) != 3 {
		t.Fatalf("expected 3 tool unions, got %d", len(params.Tools))
	}
	raw, err := json.Marshal(params.Tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if got := strings.Count(string(raw), `"defer_loading":true`); got != 2 {
		t.Fatalf("expected exactly 2 defer_loading flags, got %d: %s", got, raw)
	}
	// cache_control must sit on the last NON-deferred tool only.
	var wire []struct {
		Name         string           `json:"name"`
		DeferLoading bool             `json:"defer_loading"`
		CacheControl *json.RawMessage `json:"cache_control"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal tools wire: %v", err)
	}
	for i, wt := range wire {
		isDeferred := wt.DeferLoading
		hasCache := wt.CacheControl != nil
		if isDeferred && hasCache {
			t.Fatalf("deferred tool %q must not carry a cache breakpoint", wt.Name)
		}
		if i == len(wire)-1 && !isDeferred && !hasCache {
			t.Fatalf("last non-deferred tool %q must carry the cache breakpoint", wt.Name)
		}
		if i < len(wire)-1 && hasCache {
			t.Fatalf("non-last tool %q must not carry the cache breakpoint", wt.Name)
		}
	}
	// All-deferred edge: no breakpoint anywhere, request still valid.
	all := p.buildParams(context.Background(), []Message{}, []ToolDefinition{
		{Name: "mcp__srv__c", Description: "c", Parameters: json.RawMessage(`{"type":"object"}`), DeferLoading: true},
	})
	allRaw, _ := json.Marshal(all.Tools)
	if strings.Contains(string(allRaw), "cache_control") {
		t.Fatalf("all-deferred tools must carry no breakpoint: %s", allRaw)
	}
}
