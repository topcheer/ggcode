//go:build linux

package tool

import (
	"strings"
	"testing"
)

func TestIssue3130_ATSPIScriptGuardsDeathProneCalls(t *testing.T) {
	src := atspiScript

	body := src
	if i := strings.Index(body, "import gi"); i >= 0 {
		body = body[i:]
	}

	if strings.Contains(body, ".get_child_count()") && !strings.Contains(body, "def safe_child_count") {
		t.Fatal("raw .get_child_count() present but safe_child_count helper missing")
	}

	if n := strings.Count(body, "node.get_child_count()"); n != 1 {
		t.Fatalf("expected exactly 1 guarded get_child_count (inside helper), found %d", n)
	}

	idx := strings.Index(body, "get_state_set()")
	if idx < 0 {
		t.Fatal("get_state_set call not found in script")
	}
	head := body[max3130(0, idx-200):idx]
	if !strings.Contains(head, "try:") {
		t.Fatalf("get_state_set at %d is not preceded by a try: block in its vicinity:\n%s", idx, head)
	}

	if !strings.Contains(body, "def safe_child_count") || !strings.Contains(body, "return 0") {
		t.Fatal("safe_child_count helper incomplete")
	}
}

func max3130(a, b int) int {
	if a > b {
		return a
	}
	return b
}
