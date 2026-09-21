package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestApplyToolExamplesAnnotatesMatchingTools(t *testing.T) {
	defs := []provider.ToolDefinition{
		{Name: "read_file", Description: "Read a file"},
		{Name: "mcp__tickets__create_ticket", Description: "Create ticket"},
	}
	configured := map[string][]map[string]any{
		"mcp__tickets__create_ticket": {
			map[string]any{"title": "Login page returns 500 error", "priority": "critical"},
		},
	}
	got := ApplyToolExamples(defs, configured)
	if &got[0] == &defs[0] {
		t.Fatal("expected copy-on-write slice, got same backing array")
	}
	if len(got[0].Examples) != 0 {
		t.Fatalf("unmatched tool must not gain examples: %+v", got[0])
	}
	if len(got[1].Examples) != 1 || got[1].Examples[0]["priority"] != "critical" {
		t.Fatalf("matched tool examples lost: %+v", got[1].Examples)
	}
	// Input slice is untouched (callers may reuse it, e.g. tool search).
	if len(defs[1].Examples) != 0 {
		t.Fatal("input definitions were mutated")
	}
}

func TestApplyToolExamplesNoConfigReturnsInputVerbatim(t *testing.T) {
	defs := []provider.ToolDefinition{{Name: "read_file"}}
	if got := ApplyToolExamples(defs, nil); len(got) != 1 || len(got[0].Examples) != 0 {
		t.Fatalf("nil config must pass through, got %+v", got)
	}
	if got := ApplyToolExamples(defs, map[string][]map[string]any{"other": {map[string]any{"a": 1}}}); len(got[0].Examples) != 0 {
		t.Fatalf("no name match must pass through, got %+v", got[0])
	}
	if got := ApplyToolExamples(nil, map[string][]map[string]any{"x": {map[string]any{"a": 1}}}); got != nil {
		t.Fatalf("empty defs must pass through, got %+v", got)
	}
}

func TestApplyToolExamplesSurvivesJSONRoundTrip(t *testing.T) {
	// The provider wire format must serialize the annotated examples.
	defs := []provider.ToolDefinition{{Name: "t", Examples: []map[string]any{map[string]any{"due_date": "2026-01-15"}}}}
	raw, err := json.Marshal(ApplyToolExamples(defs, map[string][]map[string]any{"t": {map[string]any{"due_date": "2026-01-15"}}}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe []struct {
		Examples []map[string]any `json:"examples"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(probe) != 1 || len(probe[0].Examples) != 1 || probe[0].Examples[0]["due_date"] != "2026-01-15" {
		t.Fatalf("round trip lost examples: %s", raw)
	}
}

func TestAgentSetToolExamplesWiresAssembly(t *testing.T) {
	a := &Agent{}
	if a.toolExamples != nil {
		t.Fatal("default must be nil")
	}
	a.SetToolExamples(map[string][]map[string]any{"t": {map[string]any{"a": 1}}})
	if len(a.toolExamples) != 1 {
		t.Fatalf("setter did not install map: %+v", a.toolExamples)
	}
}
