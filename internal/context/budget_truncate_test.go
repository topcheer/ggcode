package context

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #520: truncatePreview must cut on a rune boundary — a byte cut at 77 splits
// a 3-byte CJK character and renders U+FFFD garbage.
func TestTruncatePreview_CJKRuneSafe(t *testing.T) {
	// 80 CJK chars = 240 bytes > 80, triggers truncation.
	s := strings.Repeat("汉", 80)
	got := truncatePreview(s)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated preview is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ... suffix, got %q", got)
	}
	// 77 bytes = 25 runes + 2 bytes; rune-safe cut keeps 25 whole runes.
	if want := strings.Repeat("汉", 25) + "..."; got != want {
		t.Fatalf("expected %d whole CJK runes + ..., got %q (len=%d)", 25, got, len(got))
	}
	// ASCII behavior unchanged.
	if got := truncatePreview(strings.Repeat("a", 100)); got != strings.Repeat("a", 77)+"..." {
		t.Fatalf("ASCII truncation changed: %q", got)
	}
	// Short strings untouched.
	if got := truncatePreview("短句"); got != "短句" {
		t.Fatalf("short string should not be truncated: %q", got)
	}
}
