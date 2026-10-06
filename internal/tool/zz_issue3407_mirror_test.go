package tool

// #3407 mirror-defect probes: the JS state chain returns a BOOLEAN true on
// a successfully checked checkbox/radio/option; the old string-typed
// Evaluate target made chromedp's json.Unmarshal fail every time, so
// successful checks were reported as "no observable effect" 100% of the
// time. stateFromRaw dispatches by concrete type - these probes pin the
// dispatch (bool true -> "true", strings pass through, nil/numbers carry
// no signal) without needing a live Chrome.

import (
	"strings"
	"testing"
)

func TestIssue3407_CheckedBoolBecomesTruthyState(t *testing.T) {
	if got := stateFromRaw(true); got != "true" {
		t.Fatalf("checked checkbox (JSON true) must surface as \"true\", got %q", got)
	}
	// And "true" != "" (unchecked baseline) -> clickEffectNote's
	// stateAfter != stateBefore comparison confirms the click.
	if got := stateFromRaw(false); got != "" {
		t.Fatalf("unchecked (JSON false) must stay empty, got %q", got)
	}
}

func TestIssue3407_StringAttributesPassThrough(t *testing.T) {
	cases := map[string]string{
		"true":  "true",  // aria-expanded="true"
		"false": "false", // aria-expanded="false" (still a state CHANGE signal)
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
	// The sentinel path: a failed state probe (before OR after the click)
	// must not let clickEffectNote's state branch confirm on the other
	// side's value - mirror of the urlBefore != "" hardening (#3398
	// review note carried in the #3407 claim).
	note := clickEffectNote("", "", "", stateProbeFailed) // probe failed after click
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("failed probe must not confirm, got %q", note)
	}
	note = clickEffectNote("", "", stateProbeFailed, "true") // baseline probe failed
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("failed baseline probe must not confirm on a non-empty after value, got %q", note)
	}
}
