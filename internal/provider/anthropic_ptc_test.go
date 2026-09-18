package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// ptcToolUseJSON is a verbatim Anthropic response block whose tool_use was
// invoked by the code execution container (programmatic tool calling).
const ptcToolUseJSON = `{"type":"tool_use","id":"toolu_ptc1","name":"read_file",` +
	`"input":{"path":"x.go"},"caller":{"type":"code_execution_20260120","tool_id":"srvtoolu_ce1"}}`

func ptcBlockUnion(t *testing.T, raw string) anthropic.ContentBlockUnion {
	t.Helper()
	var blk anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(raw), &blk); err != nil {
		t.Fatalf("unmarshal content block: %v", err)
	}
	return blk
}

func TestConvertAnthropicResponseCarriesPTCCaller(t *testing.T) {
	blocks := convertAnthropicResponse([]anthropic.ContentBlockUnion{ptcBlockUnion(t, ptcToolUseJSON)})
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.Type != "tool_use" || b.ToolID != "toolu_ptc1" {
		t.Fatalf("unexpected block: type=%q id=%q", b.Type, b.ToolID)
	}
	if len(b.CallerRaw) == 0 {
		t.Fatal("CallerRaw not carried through convertAnthropicResponse")
	}
	var probe struct {
		Type   string `json:"type"`
		ToolID string `json:"tool_id"`
	}
	if err := json.Unmarshal(b.CallerRaw, &probe); err != nil {
		t.Fatalf("unmarshal CallerRaw: %v", err)
	}
	if probe.Type != "code_execution_20260120" || probe.ToolID != "srvtoolu_ce1" {
		t.Fatalf("verbatim caller lost: %+v", probe)
	}
}

func TestConvertAnthropicResponseNoCallerLeavesRawNil(t *testing.T) {
	const noCaller = `{"type":"tool_use","id":"toolu_plain","name":"read_file","input":{}}`
	blocks := convertAnthropicResponse([]anthropic.ContentBlockUnion{ptcBlockUnion(t, noCaller)})
	if len(blocks) != 1 || len(blocks[0].CallerRaw) != 0 {
		t.Fatalf("expected nil CallerRaw, got %q", string(blocks[0].CallerRaw))
	}
}

func TestToolUseBlockParamRestoresPTCCaller(t *testing.T) {
	caller := json.RawMessage(`{"type":"code_execution_20260120","tool_id":"srvtoolu_ce1"}`)
	u := toolUseBlockParam("toolu_x", map[string]any{"path": "x.go"}, "read_file", caller)
	if u.OfToolUse == nil {
		t.Fatal("expected OfToolUse param")
	}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"code_execution_20260120"`) ||
		!strings.Contains(string(data), "srvtoolu_ce1") {
		t.Fatalf("caller not restored in request block: %s", data)
	}

	// Without callerRaw the plain constructor is used (no caller field).
	plain := toolUseBlockParam("toolu_y", map[string]any{}, "read_file", nil)
	data, err = json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}
	if strings.Contains(string(data), `"caller"`) {
		t.Fatalf("plain tool_use must omit caller: %s", data)
	}
}

func TestIsProgrammaticCaller(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{``, false},
		{`null`, false},
		{`{"type":"direct"}`, false},
		{`{"type":"code_execution_20250825","tool_id":"t"}`, true},
		{`{"type":"code_execution_20260120","tool_id":"t"}`, true},
		{`{bad json`, false},
	}
	for _, c := range cases {
		if got := isProgrammaticCaller(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("isProgrammaticCaller(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestServerToolBlockParamCodeExecutionResult(t *testing.T) {
	// PTC: code execution results must round-trip like web server-tool results
	// or the API rejects the replayed exchange.
	const raw = `{"type":"code_execution_tool_result","tool_use_id":"srvtoolu_ce1",` +
		`"content":[{"type":"code_execution_result_block","content":"42"}]}`
	u, err := serverToolBlockParam(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("serverToolBlockParam: %v", err)
	}
	if u.OfCodeExecutionToolResult == nil {
		t.Fatal("expected OfCodeExecutionToolResult param")
	}
	if u.OfCodeExecutionToolResult.ToolUseID != "srvtoolu_ce1" {
		t.Fatalf("tool_use_id mismatch: %q", u.OfCodeExecutionToolResult.ToolUseID)
	}
}
