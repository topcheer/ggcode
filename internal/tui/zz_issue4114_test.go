package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

// #4114: the /forecast task preview truncated by BYTES (shown[:60]),
// cutting CJK runes mid-sequence; %q then escaped the invalid bytes as
// \xe6 litter. The preview must be rune-safe and printable.
func TestIssue4114ForecastPreviewRuneSafe(t *testing.T) {
	prompt := "a" + strings.Repeat("汉", 21) // 64 bytes, 22 runes

	shown := util.Truncate(prompt, 60)
	if len([]rune(prompt)) > 60 {
		shown += "..."
	}

	if !utf8.ValidString(shown) {
		t.Fatalf("preview must be valid UTF-8, got %q", shown)
	}
	// 22 runes < 60 cap: full text, no ellipsis, CJK intact.
	if shown != prompt {
		t.Fatalf("under-cap CJK prompt must pass through, got %q", shown)
	}

	// Over-cap form: rune-count truncation, still valid, ellipsis appended.
	long := strings.Repeat("汉", 100)
	shown2 := util.Truncate(long, 60)
	if len([]rune(long)) > 60 {
		shown2 += "..."
	}
	if !utf8.ValidString(shown2) {
		t.Fatalf("over-cap preview must be valid UTF-8, got %q", shown2)
	}
	if got := len([]rune(shown2)); got != 63 { // 60 runes + "..." (3 runes)
		t.Fatalf("over-cap preview = 60 runes + ellipsis, got %d", got)
	}
}
