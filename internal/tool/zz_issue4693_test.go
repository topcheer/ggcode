package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression guard for #4693: arguments = JSON literal null (weak models
// omitting all params serialize to null, not {}) made json.Unmarshal leave
// argMap nil with err == nil, and the argMap["description"] assignment then
// panicked (assignment to entry in nil map). The call was caught by the outer
// recover and surfaced as a meaningless "tool panicked" error instead of the
// fallback label. null args is exactly the "description missing" shape this
// fallback exists for - it must inject the label, not panic.
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
	// Sanity: a real label (non-empty string) was produced.
	var s string
	if err := json.Unmarshal(v, &s); err != nil || strings.TrimSpace(s) == "" {
		t.Fatalf("injected description is not a non-empty string: %s", out)
	}
}
