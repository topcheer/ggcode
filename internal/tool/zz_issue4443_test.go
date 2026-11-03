package tool

import (
	"encoding/json"
	"testing"
)

func TestIssue4443MidDoubleStarCrossesSegments(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{

		{"**/a/**/b", "x/a/p/q/b", true},
		{"**/a/**/b", "a/b", true},
		{"**/a/**/b", "x/a/b", true},
		{"**/a/**/b", "x/y/a/b/c", false},
		{"src/**/a/**/b", "src/a/b", true},
		{"src/**/a/**/b", "src/x/a/y/z/b", true},
		{"src/**/a/**/b", "out/src/a/b", false},

		{"src/**", "src/x/y/z.go", true},
		{"src/**/test", "src/a/b/test", true},
		{"**/secrets/**", "a/b/secrets/c/key.pem", true},
		{"**/config/prod.yaml", "x/y/config/prod.yaml", true},
		{"src/*-gen/**", "src/a-gen/out.txt", true},
	}
	for _, c := range cases {
		got, err := matchDoubleStar(c.pattern, c.name)
		if err != nil {
			t.Fatalf("matchDoubleStar(%q, %q): %v", c.pattern, c.name, err)
		}
		if got != c.want {
			t.Errorf("matchDoubleStar(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestIssue4443EnumCorrectionRejectsNonStringAndEscapes(t *testing.T) {

	schema := json.RawMessage(`{"properties":{"mode":{"type":"string","enum":["truthy","falsy"]}}}`)
	args := json.RawMessage(`{"mode": true}`)
	out := CoerceEnumValues(schema, args)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("reparse corrected args: %v", err)
	}
	if string(m["mode"]) != "true" {
		t.Fatalf("boolean value must pass through untouched, got %s", m["mode"])
	}
}

func TestIssue4443EnumCorrectionDecodesEscapes(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"format":{"type":"string","enum":["default","json"]}}}`)

	args := json.RawMessage(`{"format": "js\non"}`)
	out := CoerceEnumValues(schema, args)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("reparse corrected args: %v", err)
	}
	if string(m["format"]) != `"json"` {
		t.Fatalf("escaped string should correct to \"json\", got %s", m["format"])
	}
}
