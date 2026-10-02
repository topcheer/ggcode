//go:build linux

package tool

import (
	"strings"
	"testing"
)

// #3130 probe: the AT-SPI python probe must be syntactically valid AND must
// not call the death-prone accessors bare (outside try). A dying app's DBus
// objects vanish mid-walk; every bare get_child_count / get_state_set /
// get_child_at_index raised GLib.Error, killed python3 with a non-zero
// exit, and failed the WHOLE find/snapshot instead of skipping the
// defunct window.
func TestIssue3130_ATSPIScriptGuardsDeathProneCalls(t *testing.T) {
	src := atspiScript

	// The script body is inside a Go raw string; extract the python part
	// after the shebang-ish header lines up to the closing backtick.
	body := src
	if i := strings.Index(body, "import gi"); i >= 0 {
		body = body[i:]
	}

	// 1. Every get_child_count call must go through the guarded helper.
	if strings.Contains(body, ".get_child_count()") && !strings.Contains(body, "def safe_child_count") {
		t.Fatal("raw .get_child_count() present but safe_child_count helper missing")
	}
	// The ONLY textual occurrence should be inside the helper definition.
	if n := strings.Count(body, "node.get_child_count()"); n != 1 {
		t.Fatalf("expected exactly 1 guarded get_child_count (inside helper), found %d", n)
	}

	// 2. get_state_set / StateType.ACTIVE check must sit inside a try
	// (they are app-death prone during the active-window scan). We assert
	// the shape: a try: line before them within 3 lines.
	idx := strings.Index(body, "get_state_set()")
	if idx < 0 {
		t.Fatal("get_state_set call not found in script")
	}
	head := body[max3130(0, idx-200):idx]
	if !strings.Contains(head, "try:") {
		t.Fatalf("get_state_set at %d is not preceded by a try: block in its vicinity:\n%s", idx, head)
	}

	// 3. Structural sanity: the guard helper itself swallows exceptions.
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
