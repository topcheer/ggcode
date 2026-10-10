package agentruntime

// #3868 probes: enum membership enforced on the accept path with native-type
// restoration, whole-string numeric parsing, integer integrality, and the
// empty-string enum member rejected at schema validation.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/tool"
)

func issue3868Schema() mcp.ElicitationSchema {
	return mcp.ElicitationSchema{
		Type: "object",
		Properties: map[string]mcp.ElicitationFieldSchema{
			"replicas": {Type: "integer", Enum: []any{float64(1), float64(2), float64(3)}},
			"verdict":  {Type: "string", Enum: []any{"go", "no-go"}},
			"count":    {Type: "number"},
			"shards":   {Type: "integer"},
		},
		Required: []string{"replicas", "verdict"},
	}
}

func TestIssue3868_FreeformOffEnumRejected(t *testing.T) {
	schema := issue3868Schema()
	// A violating surface feeds freeform text into an enum field.
	resp := tool.AskUserResponse{Answers: []tool.AskUserAnswer{
		{ID: "replicas", FreeformText: "999"},
		{ID: "verdict", SelectedChoices: []string{"go"}},
	}}
	content := buildElicitationContent(schema, resp)
	if _, ok := content["replicas"]; ok {
		t.Fatal("freeform value outside the enum must not reach the server")
	}
	if content["verdict"] != "go" {
		t.Fatalf("in-enum choice must pass, got %v", content["verdict"])
	}
}

func TestIssue3868_EnumNativeTypeRestored(t *testing.T) {
	schema := issue3868Schema()
	resp := tool.AskUserResponse{Answers: []tool.AskUserAnswer{
		{ID: "replicas", SelectedChoices: []string{"2"}},
	}}
	content := buildElicitationContent(schema, resp)
	v, ok := content["replicas"]
	if !ok {
		t.Fatal("selected enum member missing")
	}
	if f, isF := v.(float64); !isF || f != 2 {
		t.Fatalf("numeric enum must return float64(2), got %#v", v)
	}
}

func TestIssue3868_TrailingGarbageRejected(t *testing.T) {
	schema := issue3868Schema()
	resp := tool.AskUserResponse{Answers: []tool.AskUserAnswer{
		{ID: "count", FreeformText: "12 months"},
	}}
	if _, ok := buildElicitationContent(schema, resp)["count"]; ok {
		t.Fatal(`"12 months" must not parse as the number 12`)
	}
	resp = tool.AskUserResponse{Answers: []tool.AskUserAnswer{{ID: "count", FreeformText: "12.5"}}}
	if v := buildElicitationContent(schema, resp)["count"]; v == nil || v.(float64) != 12.5 {
		t.Fatalf("clean decimal must still parse, got %v", v)
	}
}

func TestIssue3868_IntegerIntegralityEnforced(t *testing.T) {
	schema := issue3868Schema()
	resp := tool.AskUserResponse{Answers: []tool.AskUserAnswer{
		{ID: "shards", FreeformText: "12.7"},
	}}
	if _, ok := buildElicitationContent(schema, resp)["shards"]; ok {
		t.Fatal("non-integral value must not ride through an integer field")
	}
}

func TestIssue3868_EmptyEnumMemberRejectedAtSchema(t *testing.T) {
	s := issue3868Schema()
	s.Properties["bad"] = mcp.ElicitationFieldSchema{Type: "string", Enum: []any{"", "x"}}
	if err := mcp.ValidateElicitationSchema(s); err == nil {
		t.Fatal("empty-string enum member must fail schema validation")
	}
	if err := mcp.ValidateElicitationSchema(issue3868Schema()); err != nil {
		t.Fatalf("clean schema must pass: %v", err)
	}
}
