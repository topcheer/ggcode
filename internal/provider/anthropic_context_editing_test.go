package provider

import (
	"encoding/json"
	"testing"
)

func TestParseContextEditing(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" = nil
	}{
		{"", ""},
		{"off", ""},
		{"  tool_results ", "tool_results"},
		{"THINKING", "thinking"},
		{"All", "all"},
		{"bogus", ""},
	}
	for _, c := range cases {
		got := ParseContextEditing(c.in)
		if c.want == "" {
			if got != nil {
				t.Fatalf("ParseContextEditing(%q) = %+v, want nil", c.in, got)
			}
			continue
		}
		if got == nil || got.Mode != c.want {
			t.Fatalf("ParseContextEditing(%q) = %+v, want mode %q", c.in, got, c.want)
		}
	}
}

func TestContextManagementPayload(t *testing.T) {
	if got := contextManagementPayload(nil); got != nil {
		t.Fatalf("nil config must produce nil payload, got %v", got)
	}
	if got := contextManagementPayload(&ContextEditingConfig{Mode: "bogus"}); got != nil {
		t.Fatalf("invalid mode must produce nil payload, got %v", got)
	}

	// tool_results mode with full tuning.
	p := contextManagementPayload(&ContextEditingConfig{
		Mode:               "tool_results",
		TriggerTokens:      50000,
		KeepToolUses:       2,
		ClearAtLeastTokens: 10000,
		ClearToolInputs:    true,
		ExcludeTools:       []string{"bash", "read_file"},
	})
	edits := p["edits"].([]map[string]any)
	if len(edits) != 1 {
		t.Fatalf("tool_results mode: want 1 edit, got %d", len(edits))
	}
	e := edits[0]
	if e["type"] != contextEditStrategyToolUses {
		t.Fatalf("strategy type = %v", e["type"])
	}
	if tr := e["trigger"].(map[string]any); tr["type"] != "input_tokens" || tr["value"] != int64(50000) {
		t.Fatalf("trigger = %v", e["trigger"])
	}
	if k := e["keep"].(map[string]any); k["type"] != "tool_uses" || k["value"] != int64(2) {
		t.Fatalf("keep = %v", e["keep"])
	}
	if cal := e["clear_at_least"].(map[string]any); cal["value"] != int64(10000) {
		t.Fatalf("clear_at_least = %v", e["clear_at_least"])
	}
	if e["clear_tool_inputs"] != true {
		t.Fatalf("clear_tool_inputs = %v", e["clear_tool_inputs"])
	}
	if ex := e["exclude_tools"].([]string); len(ex) != 2 || ex[0] != "bash" {
		t.Fatalf("exclude_tools = %v", e["exclude_tools"])
	}
	// Payload must serialize cleanly (this exact map is handed to WithJSONSet).
	if _, err := json.Marshal(p); err != nil {
		t.Fatalf("payload not JSON-serializable: %v", err)
	}

	// thinking mode with defaults.
	p = contextManagementPayload(&ContextEditingConfig{Mode: "thinking"})
	edits = p["edits"].([]map[string]any)
	if len(edits) != 1 || edits[0]["type"] != contextEditStrategyThinking {
		t.Fatalf("thinking mode payload = %v", p)
	}
	if _, ok := edits[0]["keep"]; ok {
		t.Fatalf("default keep must be omitted (API default applies)")
	}

	// all mode: both strategies.
	p = contextManagementPayload(&ContextEditingConfig{Mode: "all"})
	if len(p["edits"].([]map[string]any)) != 2 {
		t.Fatalf("all mode must carry both strategies, got %v", p)
	}
}

func TestParseAppliedEdits(t *testing.T) {
	if _, ok := parseAppliedEdits([]byte(`{"id":"msg_1"}`)); ok {
		t.Fatalf("response without context_management must report no edits")
	}
	raw := `{"id":"msg_1","context_management":{"applied_edits":[{"type":"clear_tool_uses_20250919","cleared_tool_uses":4,"cleared_input_tokens":12345},{"type":"clear_thinking_20251015","cleared_thinking_turns":1,"cleared_input_tokens":500}]}}`
	summary, ok := parseAppliedEdits([]byte(raw))
	if !ok {
		t.Fatalf("applied edits not detected")
	}
	want := "context editing cleared 4 tool result(s), 1 thinking turn(s) (~12845 input tokens)"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
	// Malformed JSON must be silent, not an error.
	if _, ok := parseAppliedEdits([]byte("not json")); ok {
		t.Fatalf("malformed payload must report no edits")
	}
	// Empty applied_edits array.
	if _, ok := parseAppliedEdits([]byte(`{"context_management":{"applied_edits":[]}}`)); ok {
		t.Fatalf("empty applied_edits must report no edits")
	}
}

func TestToggleBetaToken(t *testing.T) {
	if got := toggleBetaToken("", anthropicContextManagementBeta, true); got != anthropicContextManagementBeta {
		t.Fatalf("add to empty = %q", got)
	}
	if got := toggleBetaToken("context-management-2025-06-27", anthropicContextManagementBeta, true); got != anthropicContextManagementBeta {
		t.Fatalf("add twice must stay idempotent, got %q", got)
	}
	if got := toggleBetaToken("structured-outputs-2025-11-13,context-management-2025-06-27", anthropicContextManagementBeta, false); got != "structured-outputs-2025-11-13" {
		t.Fatalf("remove must preserve other tokens, got %q", got)
	}
	if got := toggleBetaToken("context-management-2025-06-27", anthropicContextManagementBeta, false); got != "" {
		t.Fatalf("remove last token = %q, want empty", got)
	}
}
