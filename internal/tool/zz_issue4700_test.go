package tool

import (
	"testing"
)

func TestIssue4700_EmptyEnumCandidateDoesNotPanic(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"mode":{"type":"string","enum":["","on","off"]},"required":["mode"]}}`)

	out := CoerceEnumValues(schema, []byte(`{"mode":"x"}`))
	if got := string(out); got != `{"mode":"x"}` {
		t.Errorf("single-char value should stay uncorrected, got %s", got)
	}

	out = CoerceEnumValues(schema, []byte(`{"mode":""}`))
	if got := string(out); got != `{"mode":""}` {
		t.Errorf("empty provided value should stay untouched (exact match), got %s", got)
	}

	out = CoerceEnumValues(schema, []byte(`{"mode":"OFF"}`))
	if got := string(out); got != `{"mode":"off"}` {
		t.Errorf("case correction should still apply, got %s", got)
	}
}
