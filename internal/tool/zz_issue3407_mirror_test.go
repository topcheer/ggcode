package tool

import (
	"strings"
	"testing"
)

func TestIssue3407_CheckedBoolBecomesTruthyState(t *testing.T) {
	if got := stateFromRaw(true); got != "true" {
		t.Fatalf("checked checkbox (JSON true) must surface as \"true\", got %q", got)
	}

	if got := stateFromRaw(false); got != "" {
		t.Fatalf("unchecked (JSON false) must stay empty, got %q", got)
	}
}

func TestIssue3407_StringAttributesPassThrough(t *testing.T) {
	cases := map[string]string{
		"true":  "true",
		"false": "false",
	}
	for in, want := range cases {
		if got := stateFromRaw(in); got != want {
			t.Fatalf("string %q must pass through, got %q", in, got)
		}
	}
}

func TestIssue3407_NonStateValuesCarryNoSignal(t *testing.T) {
	for _, raw := range []any{nil, 42.0, map[string]any{}} {
		if got := stateFromRaw(raw); got != "" {
			t.Fatalf("%v must carry no state signal, got %q", raw, got)
		}
	}
}

func TestIssue3407_ProbeFailureNeverConfirms(t *testing.T) {

	note := clickEffectNote("", "", "", stateProbeFailed)
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("failed probe must not confirm, got %q", note)
	}
	note = clickEffectNote("", "", stateProbeFailed, "true")
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("failed baseline probe must not confirm on a non-empty after value, got %q", note)
	}
}
