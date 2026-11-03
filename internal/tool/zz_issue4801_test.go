package tool

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestIssue4801_TimeoutBreakMirrorsBudgetBreakShape(t *testing.T) {
	b, err := os.ReadFile("code_index_manager.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	toRe := regexp.MustCompile(`(?s)if ctx\.Err\(\) != nil \{.*?truncated = true.*?breakIdx = i - 1.*?break`)
	if !toRe.MatchString(src) {
		t.Fatal("#4801: the timeout branch must set breakIdx = i-1 (mirroring the loop-top budget break so files[i] lands in the overflow tail sweep); found a different shape")
	}

	topRe := regexp.MustCompile(`(?s)if maxTerms > 0 && totalTerms >= maxTerms \{.*?truncated = true.*?breakIdx = i - 1`)
	if !topRe.MatchString(src) {
		t.Fatal("#4801: the loop-top budget break no longer uses breakIdx = i-1; the timeout mirror pin needs updating")
	}

	if got := strings.Count(src, "overflow[relPath] = mtime"); got < 2 {
		t.Fatalf("#4801: expected the two mid-loop budget breaks to keep their overflow records, found %d", got)
	}
}
