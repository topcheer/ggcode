package tool

import "testing"

func TestSimplifyAndroidClassDegenerateDot(t *testing.T) {

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

	if got := simplifyAndroidClass("."); got != "." {
		t.Fatalf(`simplifyAndroidClass(".") = %q, want "." (raw lowercase)`, got)
	}
	if got := simplifyAndroidClass(".View"); got != ".view" {
		t.Fatalf(`simplifyAndroidClass(".View") = %q, want ".view" (raw lowercase)`, got)
	}
}

func TestSimplifyAndroidClassUnchangedBehavior(t *testing.T) {

	cases := map[string]string{
		"Android.Widget.Button":       "button",
		"android.widget.TextView":     "text",
		"android.widget.LinearLayout": "linear",
		"View":                        "view",
		"android.widget.":             "widget",
	}
	for in, want := range cases {
		if got := simplifyAndroidClass(in); got != want {
			t.Fatalf("simplifyAndroidClass(%q) = %q, want %q", in, got, want)
		}
	}
}
