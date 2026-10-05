package tool

// #3407 mirror-defect probes: the JS state chain returns a BOOLEAN true on
// a successfully checked checkbox/radio/option; the old string-typed
// Evaluate target made chromedp's json.Unmarshal fail every time, so
// successful checks were reported as "no observable effect" 100% of the
// time. stateFromRaw dispatches by concrete type - these probes pin the
// dispatch (bool true -> "true", strings pass through, nil/numbers carry
// no signal) without needing a live Chrome.

import "testing"

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
