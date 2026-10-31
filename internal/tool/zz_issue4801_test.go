package tool

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #4801: doBuild's ctx.Err() timeout branch used breakIdx=i without routing
// files[i] into the overflow exclusion set, while the loop-top budget break
// uses breakIdx=i-1 (tail sweep files[breakIdx+1:] then includes files[i]).
// A mid-loop timeout is not deterministically triggerable from a test
// (cancel-before-loop yields docs==0 and the Phase-6 path never runs), so
// this pins the source contract the same way zz_issue4546_test.go pins
// hard-to-trigger paths: the timeout branch must mirror the loop-top
// budget break's breakIdx=i-1 shape.
func TestIssue4801_TimeoutBreakMirrorsBudgetBreakShape(t *testing.T) {
	b, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	// The timeout branch: ctx.Err() != nil guard, then truncated=true and
	// breakIdx=i-1 (was i before the fix).
	toRe := regexp.MustCompile(`(?s)if ctx\.Err\(\) != nil \{.*?truncated = true.*?breakIdx = i - 1.*?break`)
	if !toRe.MatchString(src) {
		t.Fatal("#4801: the timeout branch must set breakIdx = i-1 (mirroring the loop-top budget break so files[i] lands in the overflow tail sweep); found a different shape")
	}

	// The budget-loop-top break it mirrors must still be i-1 (guard against
	// future drift making the two shapes diverge again).
	topRe := regexp.MustCompile(`(?s)if maxTerms > 0 && totalTerms >= maxTerms \{.*?truncated = true.*?breakIdx = i - 1`)
	if !topRe.MatchString(src) {
		t.Fatal("#4801: the loop-top budget break no longer uses breakIdx = i-1; the timeout mirror pin needs updating")
	}

	// And the two mid-loop budget paths must keep their explicit overflow
	// record + breakIdx=i form (the third truncation shape), so all three
	// paths route their current file into the exclusion set.
	if got := strings.Count(src, "overflow[relPath] = mtime"); got < 2 {
		t.Fatalf("#4801: expected the two mid-loop budget breaks to keep their overflow records, found %d", got)
	}
}
