package session

import (
	"strings"
	"testing"
)

func TestIssue3088IndexFoldTextOffsetInOriginalTextSpace(t *testing.T) {

	text := "İİİsteam bug report"
	needle := strings.ToLower("STEAM")
	idx := indexFoldText(text, needle)
	if idx < 0 {
		t.Fatalf("expected match, got -1")
	}
	if want := 6; idx != want {
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
		{"hello world", "world", 6},
		{"HELLO World", "world", 6},
		{"HELLO World", "missing", -1},
		{"İstanbul", "istanbul", 0},
		{"", "x", -1},
		{"abc", "", 0},
		{"ünïcode SEARCH here", "search", 10},
	}
	for _, c := range cases {
		needle := strings.ToLower(c.needle)
		if got := indexFoldText(c.text, needle); got != c.want {
			t.Errorf("indexFoldText(%q, %q) = %d, want %d", c.text, needle, got, c.want)
		}
	}
}
