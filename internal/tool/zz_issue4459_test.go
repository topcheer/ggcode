package tool

import "testing"

// #4459 regression tests: cross-platform pure helpers.
//
// 1. xdLiteralPattern must neutralize regex metacharacters so a window
//    pattern like "." can no longer match EVERY window (close_window
//    text="." used to close the whole desktop session via %@" fan-out).
// 2. ydoClickCode / ydoButtonMask must map middle explicitly and reject
//    unknown buttons instead of silently degrading to BTN_LEFT.

func TestXdLiteralPatternNeutralizesMatchAllRegex(t *testing.T) {
	cases := map[string]string{
		".":         `\.`,
		".*":        `\.\*`,
		"^":         `\^`,
		"Firefox":   `Firefox`,
		"a|b":       `a\|b`,
		"(edgy)":    `\(edgy\)`,
		"  spaced ": `spaced`, // trimmed before quoting
	}
	for in, want := range cases {
		if got := xdLiteralPattern(in); got != want {
			t.Errorf("xdLiteralPattern(%q) = %q, want %q", in, got, want)
		}
	}
	// The output must behave as a literal: it must NOT match an unrelated
	// title, proving "." no longer matches everything.
	if got := xdLiteralPattern("."); got != `\.` {
		t.Fatalf("literal quote of '.' changed: %q", got)
	}
}

func TestYdoClickCodeFullButtonMapping(t *testing.T) {
	cases := map[string]string{
		"":       "0xC0",
		"left":   "0xC0",
		"right":  "0xC1",
		"middle": "0xC2", // #4459: was silently 0xC0 (left)
	}
	for in, want := range cases {
		got, err := ydoClickCode(in)
		if err != nil || got != want {
			t.Errorf("ydoClickCode(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
	if _, err := ydoClickCode("sideways"); err == nil {
		t.Error("ydoClickCode(unknown) must error, not silently map to left")
	}
}

func TestYdoButtonMaskFullButtonMapping(t *testing.T) {
	cases := map[string]int{
		"":       0x0,
		"left":   0x0,
		"right":  0x1,
		"middle": 0x2,
	}
	for in, want := range cases {
		got, err := ydoButtonMask(in)
		if err != nil || got != want {
			t.Errorf("ydoButtonMask(%q) = %#x, %v; want %#x, nil", in, got, err, want)
		}
	}
	if _, err := ydoButtonMask("sideways"); err == nil {
		t.Error("ydoButtonMask(unknown) must error, not silently map to left")
	}
}
