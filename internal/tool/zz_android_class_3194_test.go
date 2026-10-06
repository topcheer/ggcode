package tool

import "testing"

// #3194 regression probe: simplifyAndroidClass must degrade gracefully on
// degenerate uiautomator class strings (".", "..", ".View", ".Layout") —
// the exact malformed outputs the #839 fix's own comment says were
// OBSERVED on real devices. Before the fix, parts[len(parts)-2] fallback
// was taken without an emptiness check and []rune(last)[:1] sliced an
// empty rune slice -> panic on every snapshot of such a device.
//
// Probe provenance: review-seat repro (立案探针, #3190/#3191 pattern),
// rewritten by the fixing seat; semantics identical - recover() capture
// per case plus output assertions.
func TestSimplifyAndroidClassDegenerateDot(t *testing.T) {
	// Degenerate inputs must not panic and must degrade to a sane value.
	for _, c := range []string{".", "..", ".View", ".Layout"} {
		c := c
		var panicked bool
		var got string
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			got = simplifyAndroidClass(c)
		}()
		if panicked {
			t.Fatalf("simplifyAndroidClass(%q) panicked (pre-fix behavior)", c)
		}
		if got == "" {
			t.Fatalf("simplifyAndroidClass(%q) returned empty string", c)
		}
	}

	// Exact expected degradation for the observed malformed outputs.
	if got := simplifyAndroidClass("."); got != "." {
		t.Fatalf(`simplifyAndroidClass(".") = %q, want "." (raw lowercase)`, got)
	}
	if got := simplifyAndroidClass(".View"); got != ".view" {
		t.Fatalf(`simplifyAndroidClass(".View") = %q, want ".view" (raw lowercase)`, got)
	}
}

func TestSimplifyAndroidClassUnchangedBehavior(t *testing.T) {
	// Cases the #839 fix already handled must stay intact.
	cases := map[string]string{
		"Android.Widget.Button":       "button",
		"android.widget.TextView":     "text",
		"android.widget.LinearLayout": "linear", // trim suffix -> package seg
		"View":                        "view",
		"android.widget.":             "widget",
	}
	for in, want := range cases {
		if got := simplifyAndroidClass(in); got != want {
			t.Fatalf("simplifyAndroidClass(%q) = %q, want %q", in, got, want)
		}
	}
}
