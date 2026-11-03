package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue4693_NullArgsInjectsFallbackLabel(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"description":{"type":"string","description":"Brief activity label for this action"}},"required":["description"]}`)

	out := InjectDescriptionFallback(schema, json.RawMessage(`null`), "edit_file")

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not a JSON object: %v (raw: %s)", err, out)
	}
	v, ok := m["description"]
	if !ok || string(v) == `""` || string(v) == "null" {
		t.Fatalf("fallback label not injected for null args: %s", out)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil || strings.TrimSpace(s) == "" {
		t.Fatalf("injected description is not a non-empty string: %s", out)
	}
}
