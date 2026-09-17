package mcp

// SEP-1577 (MCP 2025-11-25) sampling-with-tools: wire types, array-form
// content, structural validation, and capability advertisement.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSEP1577_ParseToolSamplingRequest(t *testing.T) {
	raw := json.RawMessage(`{
		"messages": [
			{"role": "user", "content": {"type": "text", "text": "weather in Paris?"}},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "call_1", "name": "get_weather", "input": {"city": "Paris"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "toolUseId": "call_1", "content": [{"type": "text", "text": "18C sunny"}]}
			]}
		],
		"tools": [{
			"name": "get_weather",
			"description": "Get current weather",
			"inputSchema": {"type": "object", "properties": {"city": {"type": "string"}}}
		}],
		"toolChoice": {"mode": "auto"},
		"maxTokens": 1000
	}`)
	p, err := ParseSamplingParams(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Tools) != 1 || p.Tools[0].Name != "get_weather" {
		t.Fatalf("tools not parsed: %+v", p.Tools)
	}
	if p.ToolChoice == nil || p.ToolChoice.Mode != ToolChoiceAuto {
		t.Fatalf("toolChoice not parsed: %+v", p.ToolChoice)
	}
	// Assistant turn: array-form content lands in Blocks.
	if len(p.Messages[1].Blocks) != 1 || p.Messages[1].Blocks[0].ID != "call_1" {
		t.Fatalf("tool_use block not parsed: %+v", p.Messages[1])
	}
	// tool_result turn keeps nested result content.
	res := p.Messages[2].Blocks[0]
	if res.ToolUseID != "call_1" || len(res.ResultContent) != 1 || res.ResultContent[0].Text != "18C sunny" {
		t.Fatalf("tool_result block not parsed: %+v", res)
	}
	if err := ValidateSamplingParams(p); err != nil {
		t.Fatalf("valid tool sampling request rejected: %v", err)
	}
}

func TestSEP1577_ResultToolUseMarshalArrayContent(t *testing.T) {
	r := SamplingResult{
		Model:      "claude-x",
		Role:       "assistant",
		StopReason: "toolUse",
		Blocks: []SamplingContent{
			{Type: "tool_use", ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"Paris"}`)},
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	content, ok := wire["content"].([]any)
	if !ok {
		t.Fatalf("toolUse result must serialize content as array, got: %s", data)
	}
	if len(content) != 1 || wire["stopReason"] != "toolUse" {
		t.Fatalf("unexpected wire result: %s", data)
	}
	// Single-block results keep the historical object shape.
	r2 := SamplingResult{Model: "m", Role: "assistant", StopReason: "end_turn", Content: SamplingContent{Type: "text", Text: "hi"}}
	data2, _ := json.Marshal(r2)
	if strings.Contains(string(data2), `"content":[`) {
		t.Fatalf("plain result must keep object content, got: %s", data2)
	}
}

func TestSEP1577_MessageMarshalArrayContent(t *testing.T) {
	m := SamplingMessage{Role: "user", Blocks: []SamplingContent{
		{Type: "tool_result", ToolUseID: "call_1", ResultContent: []SamplingContent{{Type: "text", Text: "ok"}}},
	}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var round SamplingMessage
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatal(err)
	}
	if len(round.Blocks) != 1 || round.Blocks[0].ToolUseID != "call_1" {
		t.Fatalf("array round-trip failed: %s / %+v", data, round)
	}
}

func TestSEP1577_ValidateErrors(t *testing.T) {
	tool := SamplingTool{Name: "t", InputSchema: json.RawMessage(`{"type":"object"}`)}
	toolUse := SamplingMessage{Role: "assistant", Blocks: []SamplingContent{{Type: "tool_use", ID: "c1", Name: "t"}}}
	result := SamplingMessage{Role: "user", Blocks: []SamplingContent{{Type: "tool_result", ToolUseID: "c1"}}}

	cases := []struct {
		name    string
		params  SamplingParams
		wantSub string
	}{
		{"toolChoice without tools", SamplingParams{ToolChoice: &SamplingToolChoice{Mode: ToolChoiceAuto}}, "without tools"},
		{"bad mode", SamplingParams{Tools: []SamplingTool{tool}, ToolChoice: &SamplingToolChoice{Mode: "sometimes"}}, "not one of"},
		{"tool missing schema", SamplingParams{Tools: []SamplingTool{{Name: "t"}}}, "missing inputSchema"},
		{"duplicate tool", SamplingParams{Tools: []SamplingTool{tool, tool}}, "duplicate"},
		{"missing tool result", SamplingParams{Messages: []SamplingMessage{toolUse}}, "missing in request"},
		{"partial tool result", SamplingParams{Messages: []SamplingMessage{
			toolUse,
			{Role: "user", Blocks: []SamplingContent{{Type: "tool_result", ToolUseID: "other"}}},
		}}, "tool result missing in request"},
		{"mixed tool_result turn", SamplingParams{Messages: []SamplingMessage{
			toolUse,
			{Role: "user", Blocks: []SamplingContent{
				{Type: "text", Text: "note"},
				{Type: "tool_result", ToolUseID: "c1"},
			}},
		}}, "mixed with other content"},
		{"orphan tool_result", SamplingParams{Messages: []SamplingMessage{result}}, "without a preceding"},
		{"tool_result without id", SamplingParams{Messages: []SamplingMessage{toolUse, {Role: "user", Blocks: []SamplingContent{{Type: "tool_result"}}}}}, "missing toolUseId"},
	}
	for _, tc := range cases {
		err := ValidateSamplingParams(tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantSub, err)
		}
	}
	// Empty toolChoice mode defaults to auto.
	ok := SamplingParams{Tools: []SamplingTool{tool}, ToolChoice: &SamplingToolChoice{}}
	if err := ValidateSamplingParams(ok); err != nil {
		t.Errorf("empty toolChoice mode should default to auto, got %v", err)
	}
	// Plain text-only request stays valid (pre-SEP-1577 servers).
	plain := SamplingParams{Messages: []SamplingMessage{{Role: "user", Content: SamplingContent{Type: "text", Text: "hi"}}}}
	if err := ValidateSamplingParams(plain); err != nil {
		t.Errorf("plain request rejected: %v", err)
	}
}

func TestSEP1577_SamplingCapabilityJSON(t *testing.T) {
	caps := ClientCaps{Sampling: &SamplingCapability{Tools: &struct{}{}}}
	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sampling":{"tools":{}}`) {
		t.Fatalf("capability must declare sampling.tools per SEP-1577, got: %s", data)
	}
}
