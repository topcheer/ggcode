package extpane

import (
	"strings"
	"testing"
)

// TestCompactPreviewCapAfterExpansion pins #1368: the 100-byte cap must hold
// AFTER newline expansion (old order truncated first, then each \n grew to
// " ↵ ", blowing the cap ~5x), and ANSI sequences must never be cut mid-
// escape.
func TestCompactPreviewCapAfterExpansion(t *testing.T) {
	// 100 newlines: 100 bytes pre-expansion (passes the OLD check),
	// ~500 bytes after - must still be capped now.
	got := compactPreview(strings.Repeat("\n", 100))
	if len(got) > 100 {
		t.Fatalf("cap violated after newline expansion: %d bytes", len(got))
	}

	// CSI sequence straddling the cut boundary must be stripped first -
	// the old byte-truncation left "\x1b[3" behind.
	colored := strings.Repeat("a", 90) + "\x1b[31mRED\x1b[0m" + strings.Repeat("b", 50)
	got = compactPreview(colored)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("partial escape left in preview: %q", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}
