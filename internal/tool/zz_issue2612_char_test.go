package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #2612 characterization: pin the Execute dispatch layer's argument
// validation before the handler-table refactor. Each case asserts the
// exact error copy for a missing required argument; the refactor must
// keep every message byte-identical.
func TestBrowserExecuteValidationCharacterization(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"navigate missing url", `{"action":"navigate"}`, "url is required for navigate action"},
		{"click missing selector", `{"action":"click"}`, "selector is required for click action"},
		{"type missing selector", `{"action":"type"}`, "selector is required for type action"},
		{"evaluate missing expression", `{"action":"evaluate"}`, "expression is required for evaluate action"},
		{"wait missing wait_for", `{"action":"wait"}`, "wait_for selector is required for wait action"},
		{"select missing selector", `{"action":"select"}`, "selector is required for select action"},
		{"hover missing selector", `{"action":"hover"}`, "selector is required for hover action"},
		{"press missing key", `{"action":"press"}`, "key is required for press action"},
		{"upload missing selector", `{"action":"upload"}`, "selector is required for upload action (the input[type=file] element)"},
		{"upload missing path", `{"action":"upload","selector":"#f"}`, "path is required for upload action"},
		{"resize missing dims", `{"action":"resize"}`, "width and height are required for resize action"},
		{"wait_not missing wait_for", `{"action":"wait_not"}`, "wait_for selector is required for wait_not action"},
		{"drag missing selector", `{"action":"drag"}`, "selector (source element) is required for drag action"},
		{"drag missing value", `{"action":"drag","selector":"#a"}`, "value (target CSS selector) is required for drag action"},
		{"unknown action", `{"action":"nope"}`, "unknown action: nope"},
	}
	b := &Browser{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := b.Execute(context.Background(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("Execute returned err %v (validation must be a Result, not err)", err)
			}
			if !result.IsError {
				t.Fatalf("expected IsError result, got: %s", result.Content)
			}
			if !strings.Contains(result.Content, tc.want) {
				t.Fatalf("error copy changed:\n want: %s\n  got: %s", tc.want, result.Content)
			}
		})
	}
}
