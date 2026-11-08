package agent

import "testing"

// #5289: gh api mutating detection must cover pflag equals forms
// (--method=DELETE, --field=k=v, --input=f) and glued shorthand values
// (-Fk=v). Previously every equals form bypassed the check and mutating
// calls were judged read-only, defeating the irreversibility gate.
func TestIrrevGhApiEqualsFormFlagsDetected(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want bool // true = read-only
	}{
		// equals-form methods must be judged mutating (the #5289 bypass)
		{"method equals DELETE", "gh api --method=DELETE /repos/o/r", false},
		{"method equals PUT", "gh api --method=PUT /repos/o/r", false},
		{"method equals lowercase delete", "gh api --method=delete /repos/o/r", false},
		// safe equals-form methods stay read-only
		{"method equals GET", "gh api --method=GET /repos/o/r", true},
		{"method equals HEAD", "gh api --method=HEAD /repos/o/r", true},
		// equals/glued field flags imply a write payload (conservative rule)
		{"field equals", "gh api --field=name=x /repos/o/r", false},
		{"typed field glued", "gh api -Fname=v /repos/o/r", false},
		{"typed field equals", "gh api -F=name=x /repos/o/r", false},
		{"raw field glued", "gh api -fname=v /repos/o/r", false},
		{"input equals", "gh api --input=f.json /repos/o/r", false},
		{"field-file equals", "gh api --field-file=f.json /repos/o/r", false},
		{"slurp equals", "gh api --slurp=f.json /repos/o/r", false},
		// baseline + pre-existing forms regression-locked
		{"GET default", "gh api /repos/o/r", true},
		{"method space form", "gh api --method DELETE /repos/o/r", false},
		{"X space form", "gh api -X DELETE /repos/o/r", false},
		{"X glued form", "gh api -XDELETE /repos/o/r", false},
		{"X equals form", "gh api -X=DELETE /repos/o/r", false},
		{"field space form", "gh api -f name=x /repos/o/r", false},
		{"X glued GET stays read-only", "gh api -XGET /repos/o/r", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := irrevSegmentIsReadOnly(tc.seg); got != tc.want {
				t.Fatalf("irrevSegmentIsReadOnly(%q) = %v, want %v", tc.seg, got, tc.want)
			}
		})
	}
}
