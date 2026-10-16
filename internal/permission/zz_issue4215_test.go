package permission

import "testing"

// #4215: DangerousMode must be enterable ONLY via the explicit
// --dangerously-skip-permissions CLI flag at process start. Every runtime
// entry point (switch_mode tool, /mode, tunnel mode-change, IM slash) must
// reject it. These helpers are the single source of that gate.
func TestDangerousModeNotRuntimeSelectable(t *testing.T) {
	if IsRuntimeSelectableMode(DangerousMode) {
		t.Fatal("dangerous must not be runtime-selectable")
	}
	if IsRuntimeSelectableModeName("dangerous") {
		t.Fatal(`IsRuntimeSelectableModeName("dangerous") must be false`)
	}
	if IsRuntimeSelectableModeName("DANGEROUS") {
		t.Fatal("case variants must be rejected too")
	}

	for _, ok := range []string{"supervised", "plan", "auto", "bypass", "autopilot"} {
		if !IsRuntimeSelectableModeName(ok) {
			t.Fatalf("mode %q must remain runtime-selectable", ok)
		}
	}
	// Unknown names still report false (validity check runs first).
	if IsRuntimeSelectableModeName("nonsense") {
		t.Fatal("unknown mode names must not be selectable")
	}
}
