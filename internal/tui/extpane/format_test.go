package extpane

import (
	"strings"
	"testing"
)

func TestCompactPreviewCapAfterExpansion(t *testing.T) {

	got := compactPreview(strings.Repeat("\n", 100))
	if len(got) > 100 {
		t.Fatalf("cap violated after newline expansion: %d bytes", len(got))
	}

	colored := strings.Repeat("a", 90) + "\x1b[31mRED\x1b[0m" + strings.Repeat("b", 50)
	got = compactPreview(colored)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("partial escape left in preview: %q", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}
