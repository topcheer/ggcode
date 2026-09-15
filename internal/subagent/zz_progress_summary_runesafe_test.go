package subagent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

// TestFormatProgressSummaryRuneSafeTruncation pins the #520-class fix: the
// progress summary truncation must cut on a rune boundary. The old byte cut
// at 77 split a 3-byte CJK rune mid-sequence, so a Chinese progress line
// longer than 80 bytes rendered U+FFFD in the TUI follow panel.
func TestFormatProgressSummaryRuneSafeTruncation(t *testing.T) {
	// 100 CJK runes = 300 bytes: exceeds the 80-rune cap, must truncate on
	// a rune boundary with the "..." suffix (the old byte cut at 77 split a
	// 3-byte rune and produced U+FFFD).
	long := strings.Repeat("进度", 50)
	snap := Snapshot{Status: StatusRunning, ProgressSummary: long}
	got := formatProgressSummary(snap)
	if !utf8.ValidString(got) {
		t.Fatalf("summary must be valid UTF-8 after truncation, got invalid string: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "...") {
		t.Fatalf("truncated summary must end with ..., got %q", got)
	}
	// The truncation cap applies to the summary segment (after "\n  "),
	// not the whole "[status]"-prefixed line.
	lines := strings.SplitN(got, "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("summary line missing: %q", got)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(lines[1])); n > 80 {
		t.Fatalf("summary must be at most 80 runes, got %d", n)
	}
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), "...") {
		t.Fatalf("truncated summary must end with ..., got %q", lines[1])
	}
	if !utf8.ValidString(lines[1]) {
		t.Fatalf("summary segment must be valid UTF-8: %q", lines[1])
	}
	// The full summary must survive when it fits.
	short := strings.Repeat("进度", 10) // 20 runes, 60 bytes: fits
	snap2 := Snapshot{Status: StatusRunning, ProgressSummary: short}
	if !strings.Contains(formatProgressSummary(snap2), short) {
		t.Fatal("a summary that fits must not be truncated")
	}
	// util.Truncate ASCII parity with the old byte semantics: 80 ASCII
	// runes -> untruncated; 81 -> 77 chars + "...".
	if got := util.Truncate(strings.Repeat("a", 81), 80); got != strings.Repeat("a", 77)+"..." {
		t.Fatalf("ASCII parity broken: %q", got)
	}
}
