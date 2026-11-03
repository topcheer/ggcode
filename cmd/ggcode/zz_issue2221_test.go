package main

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

	if !strings.Contains(src, "for (const t of turns) { tIn+=t.input; tOut+=t.output; tCache+=t.cache;") {
		t.Fatal("the aggregate for-loop must remain")
	}
}
