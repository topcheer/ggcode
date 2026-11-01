package tool

import (
	"strings"
	"testing"
)

// #4691: ydoClickArgs must normalize button case and REJECT unknown buttons
// instead of silently clicking LEFT. "Right"/"RIGHT" normalize to "right"
// and stay VALID (see the normalize test); only genuinely unknown values
// are rejected - "lef"/typos/empty previously fell through to BTN_LEFT.
func TestIssue4691_YdoClickArgsRejectsUnknownButton(t *testing.T) {
	for _, bad := range []string{"lef", "back", "sideways", ""} {
		if _, err := ydoClickArgs(bad, 1); err == nil {
			t.Fatalf("button %q must be rejected, got argv", bad)
		} else if !strings.Contains(err.Error(), "unknown button") {
			t.Fatalf("button %q: unexpected error %v", bad, err)
		}
	}
}

// Case/space normalization: "Right" must produce the SAME argv as "right".
func TestIssue4691_YdoClickArgsNormalizesCase(t *testing.T) {
	canonical, err := ydoClickArgs("right", 1)
	if err != nil {
		t.Fatal(err)
	}
	variant, err := ydoClickArgs("  Right  ", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) != 1 || len(variant) != 1 {
		t.Fatalf("want 1 click argv, got %d / %d", len(canonical), len(variant))
	}
	want := []string{"ydotool", "click", "0xC1"} // BTN_RIGHT
	for i := range want {
		if variant[0][i] != want[i] || canonical[0][i] != want[i] {
			t.Fatalf("right click argv wrong: canonical=%v variant=%v", canonical, variant)
		}
	}
}

// Middle stays mapped to BTN_MIDDLE and left stays BTN_LEFT.
func TestIssue4691_YdoClickArgsCodes(t *testing.T) {
	for btn, code := range map[string]string{"left": "0xC0", "middle": "0xC2"} {
		argv, err := ydoClickArgs(btn, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(argv) != 2 || argv[0][2] != code {
			t.Fatalf("button %s: want code %s x2, got %v", btn, code, argv)
		}
	}
}
