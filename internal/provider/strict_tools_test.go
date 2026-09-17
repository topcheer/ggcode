package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/topcheer/ggcode/internal/config"
)

func TestInjectAdditionalPropertiesFalse(t *testing.T) {
	in := json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"opts":{"type":"object","properties":{"limit":{"type":"number"}}},
			"items":{"type":"array","items":{"type":"object","properties":{"a":{"type":"string"}}}}
		},
		"required":["path"],
		"$defs":{"extra":{"type":"object","properties":{"x":{"type":"string"}}}}
	}`)
	out := string(InjectAdditionalPropertiesFalse(in))
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc["additionalProperties"] != false {
		t.Errorf("root additionalProperties = %v, want false", doc["additionalProperties"])
	}
	props := doc["properties"].(map[string]any)
	opts := props["opts"].(map[string]any)
	if opts["additionalProperties"] != false {
		t.Errorf("nested additionalProperties = %v, want false", opts["additionalProperties"])
	}
	items := props["items"].(map[string]any)
	item := items["items"].(map[string]any)
	if item["additionalProperties"] != false {
		t.Errorf("items additionalProperties = %v, want false", item["additionalProperties"])
	}
	defs := doc["$defs"].(map[string]any)
	if defs["extra"].(map[string]any)["additionalProperties"] != false {
		t.Errorf("$defs additionalProperties not injected")
	}
	// required/properties payload preserved
	if _, ok := props["path"]; !ok {
		t.Errorf("properties lost during injection")
	}
}

func TestInjectAdditionalPropertiesFalseExistingFalseKept(t *testing.T) {
	in := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
	var doc map[string]any
	if err := json.Unmarshal(InjectAdditionalPropertiesFalse(in), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["additionalProperties"] != false {
		t.Errorf("explicit false was rewritten: %v", doc["additionalProperties"])
	}
	if _, ok := doc["type"]; !ok {
		t.Errorf("unrelated fields lost: %v", doc)
	}
}

func TestInjectAdditionalPropertiesFalseInvalidPassthrough(t *testing.T) {
	bad := json.RawMessage(`{invalid`)
	if got := InjectAdditionalPropertiesFalse(bad); string(got) != string(bad) {
		t.Errorf("invalid JSON should pass through unchanged, got %s", got)
	}
}

func TestPrepareStrictToolSchema(t *testing.T) {
	// All top-level properties required: strict-compatible.
	okSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"description":{"type":"string"}},"required":["path","description"]}`)
	prepared, ok := PrepareStrictToolSchema("read_file", okSchema)
	if !ok {
		t.Fatalf("all-required schema should be strict-compatible")
	}
	if !strings.Contains(string(prepared), `"additionalProperties":false`) {
		t.Errorf("injection missing from prepared schema: %s", prepared)
	}

	// Optional top-level field violates the all-required rule: skip strict.
	optionalSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}`)
	if _, ok := PrepareStrictToolSchema("read_file", optionalSchema); ok {
		t.Errorf("schema with optional top-level field must not be strict-compatible")
	}

	// Invalid schema: skip strict.
	if _, ok := PrepareStrictToolSchema("x", json.RawMessage(`not-json`)); ok {
		t.Errorf("invalid schema must not be strict-compatible")
	}
}

func TestOpenAIConvertToolsStrict(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-test", 1024)
	p.SetStrictTools(map[string]bool{"read_file": true})

	tools := []ToolDefinition{
		{
			Name:        "read_file",
			Description: "read",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		{
			Name:        "grep",
			Description: "search",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
		},
	}
	converted := p.convertTools(tools)
	if len(converted) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(converted))
	}

	rf, err := json.Marshal(converted[0].Function)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rf), `"strict":true`) {
		t.Errorf("allowlisted tool missing strict:true: %s", rf)
	}
	if !strings.Contains(string(rf), `"additionalProperties":false`) {
		t.Errorf("allowlisted tool missing injected additionalProperties:false: %s", rf)
	}

	gp, err := json.Marshal(converted[1].Function)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gp), `"strict"`) {
		t.Errorf("non-allowlisted tool must not carry a strict field: %s", gp)
	}
	if strings.Contains(string(gp), `"additionalProperties"`) {
		t.Errorf("non-allowlisted tool schema must stay untouched: %s", gp)
	}
}

// TestAnthropicStrictToolSerialization pins the wire shape: strict:true on the
// tool param and additionalProperties:false inside input_schema (via
// ExtraFields, because ToolInputSchemaParam's typed fields cannot carry it).
func TestAnthropicStrictToolSerialization(t *testing.T) {
	inputSchema := anthropic.ToolInputSchemaParam{Type: "object"}
	inputSchema.ExtraFields = map[string]any{"additionalProperties": false}
	u := anthropic.ToolUnionParamOfTool(inputSchema, "read_file")
	u.OfTool.Description = anthropic.String("read")
	u.OfTool.Strict = param.NewOpt(true)

	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"strict":true`) {
		t.Errorf("missing strict:true: %s", s)
	}
	if !strings.Contains(s, `"additionalProperties":false`) {
		t.Errorf("missing input_schema.additionalProperties:false: %s", s)
	}
	// Plain (non-strict) union params stay byte-compatible with today.
	plain := anthropic.ToolUnionParamOfTool(anthropic.ToolInputSchemaParam{Type: "object"}, "grep")
	pb, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(pb), "strict") || strings.Contains(string(pb), "additionalProperties") {
		t.Errorf("non-strict tool serialization changed: %s", pb)
	}
}

func TestStrictToolsAllowResolution(t *testing.T) {
	if got := strictToolsAllow(&config.ResolvedEndpoint{}); got != nil {
		t.Errorf("strict_tools unset should disable strict, got %v", got)
	}
	got := strictToolsAllow(&config.ResolvedEndpoint{StrictTools: true})
	for _, name := range DefaultStrictTools {
		if !got[name] {
			t.Errorf("default allowlist missing %q", name)
		}
	}
	got = strictToolsAllow(&config.ResolvedEndpoint{StrictTools: true, StrictToolsAllow: []string{"grep"}})
	if len(got) != 1 || !got["grep"] {
		t.Errorf("explicit allowlist not honored: %v", got)
	}
}
