package tool

// Pure helper coverage for warp_darwin.go and ghostty_darwin.go (sa-141).
// No AppleScript / terminal interaction is performed.

import (
	"strings"
	"testing"
)

func TestParseModifiersSa141(t *testing.T) {
	if got, err := parseModifiers(""); got != "" || err != nil {
		t.Fatalf("parseModifiers(\"\") = (%q,%v)", got, err)
	}
	got, err := parseModifiers("shift, control")
	if err != nil || got != " using {shift down, control down}" {
		t.Fatalf("parseModifiers = (%q,%v)", got, err)
	}
	// Case-insensitive + trailing comma tolerated.
	got, err = parseModifiers("OPTION ,")
	if err != nil || got != " using {option down}" {
		t.Fatalf("parseModifiers(OPTION ,) = (%q,%v)", got, err)
	}
	// Only separators -> no modifiers appended.
	got, err = parseModifiers(",,")
	if err != nil || got != "" {
		t.Fatalf("parseModifiers(,,) = (%q,%v)", got, err)
	}
	// #1709: synonyms must be rejected loudly, not silently dropped.
	_, err = parseModifiers("ctrl,c")
	if err == nil || !strings.Contains(err.Error(), "unknown modifier \"ctrl\"") || !strings.Contains(err.Error(), "legal") {
		t.Fatalf("parseModifiers(ctrl,c) err = %v", err)
	}
}

func TestFormatModsSa141(t *testing.T) {
	if got := formatMods(""); got != "none" {
		t.Fatalf("formatMods(\"\") = %q", got)
	}
	if got := formatMods("shift down"); got != "shift down" {
		t.Fatalf("formatMods = %q", got)
	}
}

func TestEscapeASSa141(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"back\\slash": "back\\\\slash",
		"quo\"te":     "quo\\\"te",
		"tab\there":   "tab\\there",
		"nl\n":        "nl\\n",
		"cr\r":        "cr\\r",
		"ctl\x01":     "ctl", // C0 control runes dropped
		"":            "",
	}
	for in, want := range cases {
		if got := escapeAS(in); got != want {
			t.Errorf("escapeAS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapeShellSingleQuoteSa141(t *testing.T) {
	if got := escapeShellSingleQuote("cd 'my dir'"); got != `cd '\''my dir'\''` {
		t.Fatalf("escapeShellSingleQuote = %q", got)
	}
	if got := escapeShellSingleQuote("safe"); got != "safe" {
		t.Fatalf("escapeShellSingleQuote(safe) = %q", got)
	}
}

func TestOppositeDirSa141(t *testing.T) {
	pairs := map[string]string{"right": "left", "left": "right", "down": "up", "up": "down"}
	for in, want := range pairs {
		if got := oppositeDir(in); got != want {
			t.Errorf("oppositeDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := oppositeDir("sideways"); got != "sideways" {
		t.Fatalf("oppositeDir passthrough = %q", got)
	}
}

func TestTerminalSpecifierSa141(t *testing.T) {
	if got := terminalSpecifier(""); got != "focused terminal of selected tab of window 1" {
		t.Fatalf("terminalSpecifier(\"\") = %q", got)
	}
	got := terminalSpecifier(`term"x`)
	if !strings.HasPrefix(got, "first terminal whose id is ") || !strings.Contains(got, `\"`) {
		t.Fatalf("terminalSpecifier escaping = %q", got)
	}
}
