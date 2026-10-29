package tool

import (
	"encoding/json"
	"testing"
)

// #4443 Bug 1: a mid-pattern '**' used to degrade to single-segment glob
// matching (never crossing '/'), so '**/a/**/b' failed open on deep paths
// and 'src/**/a/**/b' never matched at all.
func TestIssue4443MidDoubleStarCrossesSegments(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		// The issue's exact failure: '**' must swallow p/q between a and b.
		{"**/a/**/b", "x/a/p/q/b", true},
		{"**/a/**/b", "a/b", true},         // zero-segment '**' both sides
		{"**/a/**/b", "x/a/b", true},       // zero-segment second '**'
		{"**/a/**/b", "x/y/a/b/c", false},  // b not final segment
		{"src/**/a/**/b", "src/a/b", true}, // prefixed mid-** both zero
		{"src/**/a/**/b", "src/x/a/y/z/b", true},
		{"src/**/a/**/b", "out/src/a/b", false}, // prefix must anchor
		// Regression guards for the documented prior behaviors (#445/#830/#4089).
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

// #4443 Bug 3: polluted provided values must NOT be silently rewritten to
// an enum member; unparseable/non-string raw values skip correction.
func TestIssue4443EnumCorrectionRejectsNonStringAndEscapes(t *testing.T) {
	// Non-string JSON (boolean) with an enum containing near strings
	// ("truthy" is Levenshtein-close to "true"): old Trim-based path fed
	// the raw "true" text in and could rewrite; now unmarshal-to-string
	// fails and correction is skipped entirely.
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

// #4443 Bug 3 companion: a legit string with an embedded escape keeps its
// decoded form when corrected (JSON round-trip, no backslash debris).
func TestIssue4443EnumCorrectionDecodesEscapes(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"format":{"type":"string","enum":["default","json"]}}}`)
	// "js\non" (decoded js+LF+on) is Levenshtein-2 from "json".
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
