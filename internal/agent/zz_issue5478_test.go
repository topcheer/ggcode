package agent

import "testing"

// Issue #5478 pin (third "0 grounding" FP layer): a substitution whose BODY
// is read-only must not gate the whole segment. The standard patrol shape is
// `gh api repos/o/r/commits/$(gh pr view N --json headRefOid -q .headRefOid)/check-runs`
// - the $( ) only injects a SHA. Mutating bodies still gate; redirects and
// unbalanced substitutions stay gated.
func TestIssue5478SubstitutionBodyParity(t *testing.T) {
	cases := []struct {
		seg  string
		want bool
	}{
		{"gh api repos/o/r/commits/$(gh pr view 5 --json headRefOid -q .headRefOid)/check-runs", true}, // R4-22 patrol shape
		{"gh pr view 5475 --json state --jq '.state'", true},                                           // plain patrol regression
		{"echo $(gh pr list --state open)", true},                                                      // read-only body in $()
		{"echo `gh pr list --state open`", true},                                                       // read-only body in backticks
		{"gh api repos/o/r --jq \"$(gh pr list)\"", true},                                              // substitution inside quoted jq
		{"echo $(git push origin main)", false},                                                        // mutating body gates
		{"echo `git push origin main`", false},                                                         // mutating backtick body gates
		{"echo $(gh pr list", false},                                                                   // unbalanced stays gated
		{"echo `gh pr list", false},                                                                    // unbalanced backtick stays gated
		{"echo hi > out.txt", false},                                                                   // redirect keeps hard gate
	}
	for _, c := range cases {
		if got := irrevSegmentIsReadOnly(c.seg); got != c.want {
			t.Errorf("irrevSegmentIsReadOnly(%q) = %v, want %v", c.seg, got, c.want)
		}
	}
}
