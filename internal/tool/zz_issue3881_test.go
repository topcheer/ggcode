//go:build linux

package tool

import (
	"strings"
	"testing"
)

// #3881: X11 drag must chain the full gesture in one xdotool invocation with
// --sync on both moves. The pre-fix form fired four independent processes and
// swallowed the first three steps' errors, so a failed mousedown followed by
// a successful mouseup reported OK for a drag that never happened.
func TestIssue3881X11DragArgsChainedWithSync(t *testing.T) {
	args := x11DragArgs(10, 20, 30, 40)

	joined := strings.Join(args, " ")
	want := "mousemove --sync 10 20 mousedown 1 mousemove --sync 30 40 mouseup 1"
	if joined != want {
		t.Fatalf("x11DragArgs = %q, want %q", joined, want)
	}
	// Both moves must be synchronous so a release cannot land at a midpoint.
	if got := strings.Count(joined, "--sync"); got != 2 {
		t.Fatalf("expected exactly 2 --sync flags (one per move), got %d in %q", got, joined)
	}
	// The gesture must end with mouseup so the button is never left pressed.
	if !strings.HasSuffix(joined, "mouseup 1") {
		t.Fatalf("drag must end with mouseup, got %q", joined)
	}
}

// #3881 (low): find_and_click/wait_and_click must honor the button param on
// both Linux backends instead of hardcoding left. The X11 branch feeds
// x11ButtonCode directly into xdotool; the Wayland branch routes through
// ydoClickArgs.
func TestIssue3881AtspiClickHonorsButtonParam(t *testing.T) {
	// X11: x11ButtonCode must map right/middle (the schema enum) distinctly
	// from the left default the old hardcoded "1" produced.
	if got, err := x11ButtonCode("right"); err != nil || got != "3" {
		t.Fatalf("x11ButtonCode(right) = %q err=%v, want 3", got, err)
	}
	if got, err := x11ButtonCode("middle"); err != nil || got != "2" {
		t.Fatalf("x11ButtonCode(middle) = %q err=%v, want 2", got, err)
	}
	if got, err := x11ButtonCode(""); err != nil || got != "1" {
		t.Fatalf("x11ButtonCode(default) = %q err=%v, want 1", got, err)
	}
	// #4896: case variants normalize; unknown values are rejected, never
	// silently downcased to LEFT.
	if got, err := x11ButtonCode("Right"); err != nil || got != "3" {
		t.Fatalf("x11ButtonCode(Right) = %q err=%v, want 3 (normalized)", got, err)
	}
	if _, err := x11ButtonCode("rgiht"); err == nil {
		t.Fatal("x11ButtonCode(typo) must be rejected, not defaulted to left")
	}

	// Wayland: ydoClickArgs must produce the matching BTN_* code, not the
	// hardcoded 0xC0 the old atspiLocate branch used.
	for button, wantCode := range map[string]string{
		"":      "0xC0", // BTN_LEFT
		"right": "0xC1", // BTN_RIGHT
	} {
		cmds, err := ydoClickArgs(button, 1)
		if err != nil || len(cmds) != 1 || len(cmds[0]) != 3 {
			t.Fatalf("ydoClickArgs(%q,1) = %v (err %v), want a single ydotool click argv", button, cmds, err)
		}
		if cmds[0][2] != wantCode {
			t.Fatalf("ydoClickArgs(%q,1) code = %q, want %q", button, cmds[0][2], wantCode)
		}
	}
}
