package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestRepairJSONAgentPipeline verifies that the RepairJSON function (now used
// in the agent's executeTool pipeline) correctly repairs common JSON
// malformations that would otherwise cause all downstream pre-processors to
// silently bail out.
func TestRepairJSONAgentPipeline(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
	}{
		{
			name:    "trailing comma",
			input:   `{"file_path": "/tmp/test.go", "old_text": "foo",}`,
			wantKey: "file_path",
			wantVal: "/tmp/test.go",
		},
		{
			name:    "markdown code fence",
			input:   "```json\n{\"file_path\": \"/tmp/test.go\"}\n```",
			wantKey: "file_path",
			wantVal: "/tmp/test.go",
		},
		{
			name:    "smart quotes",
			input:   "{\u201cfile_path\u201d: \u201c/tmp/test.go\u201d}",
			wantKey: "file_path",
			wantVal: "/tmp/test.go",
		},
		{
			name:    "truncated - missing closing brace",
			input:   `{"file_path": "/tmp/test.go", "old_text": "foo"`,
			wantKey: "file_path",
			wantVal: "/tmp/test.go",
		},
		{
			name:    "surrounding prose",
			input:   `Here are the args: {"file_path": "/tmp/test.go"} hope this works`,
			wantKey: "file_path",
			wantVal: "/tmp/test.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repaired, ok := provider.RepairJSON([]byte(tt.input))
			if !ok {
				// Some inputs might already be valid JSON after extraction
				// Let's check if the original is valid
				if json.Valid([]byte(tt.input)) {
					repaired = []byte(tt.input)
				} else {
					t.Fatalf("RepairJSON failed for %q", tt.name)
				}
			}

			var m map[string]interface{}
			if err := json.Unmarshal(repaired, &m); err != nil {
				t.Fatalf("repaired JSON is still invalid: %v\nrepaired: %s", err, string(repaired))
			}

			val, exists := m[tt.wantKey]
			if !exists {
				t.Fatalf("expected key %q in repaired JSON, got keys: %v", tt.wantKey, m)
			}
			if s, ok := val.(string); !ok || s != tt.wantVal {
				t.Fatalf("expected %q=%q, got %v", tt.wantKey, tt.wantVal, val)
			}
		})
	}
}

// TestRepairJSONAlreadyValid verifies that valid JSON is returned unchanged
// (fast path — no repair needed, no performance overhead).
func TestRepairJSONAlreadyValid(t *testing.T) {
	input := `{"file_path": "/tmp/test.go", "old_text": "foo", "new_text": "bar"}`
	_, ok := provider.RepairJSON([]byte(input))
	if ok {
		t.Fatal("RepairJSON should return false (no repair needed) for valid JSON")
	}
}

// TestRepairJSONEmpty verifies edge cases.
func TestRepairJSONEmpty(t *testing.T) {
	_, ok := provider.RepairJSON(nil)
	if ok {
		t.Fatal("RepairJSON should return false for nil input")
	}
	_, ok = provider.RepairJSON([]byte{})
	if ok {
		t.Fatal("RepairJSON should return false for empty input")
	}
	_, ok = provider.RepairJSON([]byte("   "))
	if ok {
		t.Fatal("RepairJSON should return false for whitespace-only input")
	}
}

// TestCoerceArgumentsAfterRepair verifies the integration: after JSON repair,
// schema-aware coercion should work correctly on the repaired arguments.
func TestCoerceArgumentsAfterRepair(t *testing.T) {
	// Simulate a weak model sending string "50" for an integer field with
	// trailing comma (common from vLLM/goolm).
	malformed := `{"offset": "50",}`

	// Step 1: Repair the JSON
	repaired, ok := provider.RepairJSON([]byte(malformed))
	if !ok {
		t.Fatal("RepairJSON failed")
	}

	// Step 2: Create a simple schema with integer "offset" field
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"offset": {"type": "integer"}
		}
	}`)

	// Step 3: Coerce arguments (should convert "50" -> 50)
	coerced := tool.CoerceArguments(schema, repaired)

	var m map[string]interface{}
	if err := json.Unmarshal(coerced, &m); err != nil {
		t.Fatalf("coerced args invalid: %v", err)
	}

	offset, ok := m["offset"].(float64)
	if !ok {
		t.Fatalf("expected offset to be numeric after coercion, got %T: %v", m["offset"], m["offset"])
	}
	if offset != 50 {
		t.Fatalf("expected offset=50, got %v", offset)
	}
}
