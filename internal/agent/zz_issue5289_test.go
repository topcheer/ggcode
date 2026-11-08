package agent

import "testing"

// #5289: irrevSegmentIsReadOnly's gh api branch only matched mutating
// flags in space form (--method DELETE) or glued -X form (-XPOST), so
// pflag equals forms (--method=DELETE, -X=POST, --field=k=v,
// --input=file) bypassed the gate and were misjudged read-only.

func TestIssue5289_EqualsFormMutatingFlagsDetected(t *testing.T) {
	cases := []struct {
		cmd    string
		roWant bool // want irrevSegmentIsReadOnly to say read-only
	}{
		{"gh api --method=DELETE repos/x/y", false},
		{"gh api -X=DELETE repos/x/y", false},
		{"gh api --field=name=x repos/x/y", false},
		{"gh api -F=note=1 repos/x/y", false},
		{"gh api --input=body.json repos/x/y", false},
		{"gh api --slurp=true repos/x/y", false},
		{"gh api repos/x/y", true},              // GET default stays read-only
		{"gh api --method=GET repos/x/y", true}, // explicit GET stays read-only
		{"gh api -X GET repos/x/y", true},       // existing space form untouched
		{"gh api -f name=x repos/x/y", false},   // space-form field flag is mutating
	}
	for i, c := range cases {
		got := irrevSegmentIsReadOnly(c.cmd)
		if got != c.roWant {
			t.Errorf("case %d %q: read-only=%v want %v", i, c.cmd, got, c.roWant)
		}
	}
}

func TestIssue5289_EqualsFormSpaceFormParity(t *testing.T) {
	// equals form must behave identically to the space form
	pairs := [][2]string{
		{"gh api --method=DELETE repos/x", "gh api --method DELETE repos/x"},
		{"gh api --field=k=v repos/x", "gh api --field k=v repos/x"},
		{"gh api -X=GET repos/x", "gh api -X GET repos/x"},
	}
	for i, p := range pairs {
		eq, sp := irrevSegmentIsReadOnly(p[0]), irrevSegmentIsReadOnly(p[1])
		if eq != sp {
			t.Errorf("parity %d: equals %q -> %v, space %q -> %v", i, p[0], eq, p[1], sp)
		}
	}
}
