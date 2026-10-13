package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

// #4114: /forecast task preview must be rune-safe. The old byte-based
// shown[:60]+"..." cut multi-byte UTF-8 mid-sequence for CJK prompts (byte
// budget exhausted at 20 CJK chars) and %q rendered the invalid bytes as
// \xe6 escapes.
func TestForecastPromptPreviewRuneSafe(t *testing.T) {
	// Case 1: byte-heavy but rune-short prompt (22 runes, 64 bytes). The old
	// byte cut landed inside a 3-byte CJK sequence; the rune budget of 60
	// holds all 22 runes untruncated - and still valid UTF-8.
	short := "a" + strings.Repeat("汉", 21)
	if shown := util.Truncate(short, 60); shown != short {
		t.Fatalf("22-rune prompt should pass through untruncated: %q", shown)
	}

	// Case 2: genuinely long CJK prompt (70 runes = 210 bytes). Old code cut
	// at byte 60 (inside rune 20); rune-safe truncation keeps whole runes and
	// appends "..." within the budget.
	long := strings.Repeat("汉", 70)
	shown := util.Truncate(long, 60)
	if !utf8.ValidString(shown) {
		t.Fatalf("preview is not valid UTF-8: %q", shown)
	}
	if got := len([]rune(shown)); got != 60 {
		t.Fatalf("truncated preview should be exactly 60 runes incl. '...': %d", got)
	}
	if !strings.HasSuffix(shown, "...") {
		t.Fatalf("truncated preview should end with '...': %q", shown)
	}
	// The rendered task line must keep CJK runes literally and contain no
	// %q-style byte escapes.
	rendered := "  Task: \"" + shown + "\""
	for _, r := range "汉" {
		if !strings.ContainsRune(rendered, r) {
			t.Fatalf("rendered preview lost CJK rune %q: %q", r, rendered)
		}
	}
	if strings.Contains(rendered, "\\x") {
		t.Fatalf("rendered preview contains byte-escape sequences: %q", rendered)
	}
}

// ASCII prompts pass through unchanged (no regression on the common path).
func TestForecastPromptPreviewAscii(t *testing.T) {
	prompt := strings.Repeat("x", 70)
	shown := util.Truncate(prompt, 60)
	if got := len(shown); got != 60 {
		t.Fatalf("ascii preview should be 60 chars incl. '...': %d", got)
	}
	if !strings.HasSuffix(shown, "...") {
		t.Fatalf("ascii truncated preview should end with '...': %q", shown)
	}
}
