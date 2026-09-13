package main

// #2221 case A regression: renderOverview's forEach re-summed
// tIn/tOut/tCache on top of the aggregate for-loop - every Overview card
// was exactly 2x the Daily-chart sum. The forEach legitimately collects
// ttfts and dayMap only.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2221OverviewNoDoubleCount(t *testing.T) {
	b, err := os.ReadFile("report_html.go")
	if err != nil {
		t.Skipf("layout changed: %v", err)
	}
	src := string(b)
	if strings.Contains(src, "tIn += t.input; tOut += t.output; tCache += t.cache;\n      if (t.ttftMs") {
		t.Fatal("forEach must not re-sum the aggregates (2x Overview cards)")
	}
	// The single legitimate aggregate line stays.
	if !strings.Contains(src, "for (const t of turns) { tIn+=t.input; tOut+=t.output; tCache+=t.cache;") {
		t.Fatal("the aggregate for-loop must remain")
	}
}
