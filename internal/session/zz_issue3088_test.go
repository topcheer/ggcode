package session

import (
	"strings"
	"testing"
)

// #3088: snippet offsets must be computed in original-text byte space.
// strings.ToLower lengthens some runes (U+0130 "İ" -> 3-byte "i̇"), so an
// offset found in a lowered copy drifts by the accumulated delta and the
// snippet window lands off-match or mid-rune.

func TestIssue3088IndexFoldTextOffsetInOriginalTextSpace(t *testing.T) {
	// "İ" lowercases to 3 bytes while occupying 2 bytes in text: every
	// folded index into a lowered copy drifts +1 byte per İ before the match.
	text := "İİİsteam bug report"
	needle := strings.ToLower("STEAM") // "steam"
	idx := indexFoldText(text, needle)
	if idx < 0 {
		t.Fatalf("expected match, got -1")
	}
	if want := 6; idx != want { // 3 × İ (2 bytes each) precede "steam"
		t.Errorf("idx = %d, want %d (original-text offset)", idx, want)
	}
	if text[idx:idx+len(needle)] != "steam" {
		t.Errorf("offset does not point at the match in original text: %q", text[idx:idx+len(needle)])
	}
}

func TestIssue3088SnippetContainsMatchAfterLengtheningRunes(t *testing.T) {
	text := "prefix İİİ the steam engine broke down and needs attention now"
	needle := strings.ToLower("STEAM")
	snippet := makeSnippet(text, indexFoldText(text, needle), needle)
	if !strings.Contains(snippet, "steam") {
		t.Errorf("snippet does not contain the matched word: %q", snippet)
	}
	// Fix regression guard: the pre-fix lowered-offset path drifted the
	// window past the match (snippet missing "steam" entirely).
	for _, r := range snippet {
		if r == 0xFFFD {
			t.Errorf("snippet contains replacement char (mid-rune slice): %q", snippet)
			break
		}
	}
}

func TestIssue3088IndexFoldTextBasics(t *testing.T) {
	cases := []struct {
		text, needle string
		want         int
	}{
		{"hello world", "world", 6},           // plain ASCII exact
		{"HELLO World", "world", 6},           // case fold hit
		{"HELLO World", "missing", -1},        // no match
		{"İstanbul", "istanbul", 0},           // leading lengthening rune, match at 0
		{"", "x", -1},                         // empty text
		{"abc", "", 0},                        // empty needle convention: 0
		{"ünïcode SEARCH here", "search", 10}, // multi-byte runes before ASCII match
	}
	for _, c := range cases {
		needle := strings.ToLower(c.needle)
		if got := indexFoldText(c.text, needle); got != c.want {
			t.Errorf("indexFoldText(%q, %q) = %d, want %d", c.text, needle, got, c.want)
		}
	}
}
