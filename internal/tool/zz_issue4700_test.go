package tool

import (
	"testing"
)

// Regression guard for #4700: an empty-string enum member (legal JSON Schema
// form `enum: ["", "a"]`) made findBestEnumMatch's short-value branch index
// lower[0] on an empty string once a single-char value was provided - the
// boolean chain (dist != 1 || len(provided) == 0 || lower[0] ...) never
// short-circuited before the index expression. Coercion with an empty enum
// member must never panic and must never "correct" toward the empty member.
func TestIssue4700_EmptyEnumCandidateDoesNotPanic(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"mode":{"type":"string","enum":["","on","off"]},"required":["mode"]}}`)

	// Single-char provided value: the exact shape that reached lower[0]
	// pre-fix (levenshtein("x","")==1, len(provided)!=0).
	out := CoerceEnumValues(schema, []byte(`{"mode":"x"}`))
	if got := string(out); got != `{"mode":"x"}` {
		t.Errorf("single-char value should stay uncorrected, got %s", got)
	}

	// Empty-string provided value must also be safe.
	out = CoerceEnumValues(schema, []byte(`{"mode":""}`))
	if got := string(out); got != `{"mode":""}` {
		t.Errorf("empty provided value should stay untouched (exact match), got %s", got)
	}

	// Sanity: legitimate correction still works alongside an empty member.
	out = CoerceEnumValues(schema, []byte(`{"mode":"OFF"}`))
	if got := string(out); got != `{"mode":"off"}` {
		t.Errorf("case correction should still apply, got %s", got)
	}
}
