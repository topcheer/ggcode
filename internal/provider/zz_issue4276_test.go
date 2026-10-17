package provider

import (
	"encoding/json"
	"testing"
)

// #4276: moonshot-flavored endpoints reject free-form object schema nodes
// ({"type":"object"} with neither properties nor additionalProperties) as
// "detected infinite recursion without termination condition". The gate is
// dual-axis OR (URL host OR model name) and the normalization must only ever
// ADD an explicit "additionalProperties": true - never narrow anything.

func TestIssue4276_GateDualAxis(t *testing.T) {
	cases := []struct {
		baseURL, model string
		want           bool
		why            string
	}{
		{"https://api.moonshot.cn/v1", "kimi-k2-0711-preview", true, "URL axis: moonshot.cn"},
		{"https://api.moonshot.ai/v1", "whatever-model", true, "URL axis: moonshot.ai"},
		{"https://api.kimi.com/v1", "anything", true, "URL axis: kimi.com platform"},
		{"https://api.302.ai/v1", "kimi-k2.5", true, "model axis via gateway"},
		{"https://openrouter.ai/api/v1", "moonshotai/kimi-k2.5", true, "model axis: openrouter id"},
		{"https://api.openai.com/v1", "gpt-5.2", false, "openai endpoint, openai model"},
		{"https://api.302.ai/v1", "claude-opus-4-6", false, "gateway URL, non-moonshot model"},
		{"", "", false, "empty"},
	}
	for _, c := range cases {
		if got := moonshotSchemaGateActive(c.baseURL, c.model); got != c.want {
			t.Errorf("gate(%q, %q) = %v, want %v (%s)", c.baseURL, c.model, got, c.want, c.why)
		}
	}
}

func TestIssue4276_GateCaseInsensitive(t *testing.T) {
	if !moonshotSchemaGateActive("https://API.MOONSHOT.CN/v1", "x") {
		t.Fatal("URL axis must be case-insensitive")
	}
	if !moonshotSchemaGateActive("", "Kimi-K2.5") {
		t.Fatal("model axis must be case-insensitive")
	}
}

// The exact live-failure shape from the issue: define_tool/macro steps with a
// free-form args object must gain an explicit additionalProperties:true.
func TestIssue4276_NormalizeStepsFreeFormObject(t *testing.T) {
	in := json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string"},
			"steps":{
				"type":"array",
				"items":{
					"type":"object",
					"properties":{
						"tool":{"type":"string"},
						"args":{"type":"object"}
					},
					"required":["tool"]
				}
			}
		},
		"required":["action"]
	}`)
	out := normalizeMoonshotSchema(in)
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("normalized schema not valid JSON: %v", err)
	}
	steps := doc["properties"].(map[string]any)["steps"].(map[string]any)
	items := steps["items"].(map[string]any)
	args := items["properties"].(map[string]any)["args"].(map[string]any)
	ap, ok := args["additionalProperties"]
	if !ok || ap != true {
		t.Fatalf("steps.items.properties.args must gain additionalProperties:true after normalization, got %v (ok=%v)", ap, ok)
	}
	// The flagged path in the live error ('properties.steps') now carries a
	// terminating additionalProperties on its free-form leaf.
	if _, hasProps := args["properties"]; hasProps {
		t.Fatal("normalization must not invent properties")
	}
}

func TestIssue4276_NormalizeNeverNarrows(t *testing.T) {
	// Object with explicit properties and additionalProperties:false must be
	// untouched; idempotency on already-normalized input.
	in := json.RawMessage(`{
		"type":"object",
		"properties":{
			"fixed":{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false},
			"free":{"type":"object"}
		}
	}`)
	once := string(normalizeMoonshotSchema(in))
	twice := string(normalizeMoonshotSchema(json.RawMessage(once)))
	if once != twice {
		t.Fatalf("normalization not idempotent:\nfirst:  %s\nsecond: %s", once, twice)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(once), &doc); err != nil {
		t.Fatal(err)
	}
	props := doc["properties"].(map[string]any)
	fixed := props["fixed"].(map[string]any)
	if fixed["additionalProperties"] != false {
		t.Fatalf("explicit additionalProperties:false must be preserved, got %v", fixed["additionalProperties"])
	}
	free := props["free"].(map[string]any)
	if free["additionalProperties"] != true {
		t.Fatalf("free-form object must gain additionalProperties:true, got %v", free["additionalProperties"])
	}
}

func TestIssue4276_NormalizeHandlesAnyOfAndInvalidJSON(t *testing.T) {
	// anyOf branches descend.
	in := json.RawMessage(`{"anyOf":[{"type":"object"},{"type":"string"}]}`)
	out := string(normalizeMoonshotSchema(in))
	want := `"additionalProperties":true`
	if !containsASCII(out, want) {
		t.Fatalf("anyOf free-form branch must be normalized, got %s", out)
	}
	// Invalid JSON passes through untouched (the convertTools fallback
	// upstream guarantees validity, this is defense in depth).
	if got := string(normalizeMoonshotSchema(json.RawMessage(`{invalid`))); got != `{invalid` {
		t.Fatalf("invalid JSON must pass through, got %s", got)
	}
}
