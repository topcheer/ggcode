package agent

// zz_issue4340_reaffirm_test.go -- release-side companion of the #4340
// leave-alone widening: refusalNegatePattern's gap grew to [^\n]{0,40}
// (PR #4342), so "leave config.yaml alone" now RECORDS a 30-day block.
// The #3469 reaffirm guard's parallel leave-gap arm must widen in lockstep,
// or a strong-lift release carrying the same long-object phrasing
// ("ok, leave config.yaml alone, that's exactly right") matches no reaffirm
// alternative and silently deletes the live block.

import (
	"strings"
	"testing"
)

func TestIssue4340ReaffirmCoversWidenedGap(t *testing.T) {
	for _, s := range []string{
		"leave config.yaml alone",
		"leave main.go alone",
	} {
		if !refusalReaffirmPattern.MatchString(s) {
			t.Errorf("reaffirm pattern must match %q - a strong-lift release carrying this phrasing would silently delete a live block", s)
		}
	}
	// The gap stays line-bounded and capped: a >40-char object must not
	// turn an unrelated leave-alone mention into a perpetual veto.
	long := "leave " + strings.Repeat("a", 60) + " alone"
	if refusalReaffirmPattern.MatchString(long) {
		t.Errorf("reaffirm leave-gap must stay capped at 40 chars, matched %q", long)
	}
}
