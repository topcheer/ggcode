package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// Gate-recalib defect A fix: write-tool schemas must declare the optional
// arg_rationale property (single-point injection in ToDefinitions).
func TestInjectArgRationaleSchema_WriteToolsGainField(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
	out := injectArgRationaleSchema("run_command", params)
	if !strings.Contains(string(out), "arg_rationale") {
		t.Fatalf("write tool schema must gain arg_rationale, got: %s", out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("injected schema must stay valid JSON: %v", err)
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties must be an object, got: %T", m["properties"])
	}
	if _, ok := props["command"]; !ok {
		t.Fatal("existing properties must survive injection")
	}
	if _, ok := props["arg_rationale"]; !ok {
		t.Fatal("arg_rationale must be in properties")
	}
	// Required list must not grow: the field is optional.
	if strings.Contains(string(out), `"arg_rationale"`) && strings.Contains(string(out), `"required":["command","arg_rationale"]`) {
		t.Fatal("arg_rationale must never be required")
	}
}

func TestInjectArgRationaleSchema_ReadOnlyPassesThrough(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`)
	out := injectArgRationaleSchema("grep", params)
	if strings.Contains(string(out), "arg_rationale") {
		t.Fatal("read-only tool schema must pass through unchanged")
	}
}

func TestInjectArgRationaleSchema_Idempotent(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	once := injectArgRationaleSchema("write_file", params)
	twice := injectArgRationaleSchema("write_file", once)
	if string(once) != string(twice) {
		t.Fatalf("double injection must be a no-op: %s vs %s", once, twice)
	}
}

func TestInjectArgRationaleSchema_MalformedPassesThrough(t *testing.T) {
	bad := json.RawMessage(`{not-json`)
	if got := injectArgRationaleSchema("edit_file", bad); string(got) != string(bad) {
		t.Fatalf("malformed schema must pass through untouched, got: %s", got)
	}
}
