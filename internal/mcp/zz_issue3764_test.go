package mcp

// #3764 probes: a JSON-Schema-legal numeric/boolean enum must not crash
// ParseElicitationParams at unmarshal time; formatted display for
// non-string entries.

import (
	"encoding/json"
	"testing"
)

func TestIssue3764_IntegerEnumParses(t *testing.T) {
	raw := json.RawMessage(`{"message":"pick replicas","requestedSchema":{"type":"object","properties":{"replicas":{"type":"integer","enum":[1,2,3],"description":"replica count"}},"required":["replicas"]}}`)
	p, err := ParseElicitationParams(raw)
	if err != nil {
		t.Fatalf("integer enum must parse (JSON Schema legal), got: %v", err)
	}
	field := p.Schema.Properties["replicas"]
	if len(field.Enum) != 3 || field.Enum[0] != float64(1) {
		t.Fatalf("enum entries = %#v", field.Enum)
	}
}

func TestIssue3764_BooleanEnumParses(t *testing.T) {
	raw := json.RawMessage(`{"message":"confirm","requestedSchema":{"type":"object","properties":{"force":{"type":"boolean","enum":[true,false]}},"required":["force"]}}`)
	if _, err := ParseElicitationParams(raw); err != nil {
		t.Fatalf("boolean enum must parse, got: %v", err)
	}
}

func TestIssue3764_FormatEnumValue(t *testing.T) {
	if got := FormatEnumValue("blue"); got != "blue" {
		t.Fatalf("string entry = %q", got)
	}
	if got := FormatEnumValue(float64(3)); got != "3" {
		t.Fatalf("number entry = %q", got)
	}
	if got := FormatEnumValue(true); got != "true" {
		t.Fatalf("bool entry = %q", got)
	}
}

func TestIssue3764_StringEnumStillParses(t *testing.T) {
	raw := json.RawMessage(`{"message":"pick","requestedSchema":{"type":"object","properties":{"env":{"type":"string","enum":["dev","staging","prod"]}},"required":["env"]}}`)
	p, err := ParseElicitationParams(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatEnumValue(p.Schema.Properties["env"].Enum[1]); got != "staging" {
		t.Fatalf("legacy string enum = %q", got)
	}
}

func TestIssue3764_UppercaseLocalhostHostAccepted(t *testing.T) {
	// #3764-B: host is case-insensitive (RFC 3986 6.2.2.1).
	if err := ValidateElicitationURL("http://LOCALHOST:3000/x"); err != nil {
		t.Fatalf("uppercase localhost host must be accepted as local, got: %v", err)
	}
	if err := ValidateElicitationURL("http://LocalHost:3000/x"); err != nil {
		t.Fatalf("mixed-case localhost host must be accepted, got: %v", err)
	}
}
