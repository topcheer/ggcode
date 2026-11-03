package context

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncatePreview_CJKRuneSafe(t *testing.T) {
	s := strings.Repeat("汉", 80)
	got := truncatePreview(s)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated preview is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ... suffix, got %q", got)
	}
	if want := strings.Repeat("汉", 25) + "..."; got != want {
		t.Fatalf("expected %d whole CJK runes + ..., got %q (len=%d)", 25, got, len(got))
	}
	if got := truncatePreview(strings.Repeat("a", 100)); got != strings.Repeat("a", 77)+"..." {
		t.Fatalf("ASCII truncation changed: %q", got)
	}
	if got := truncatePreview("短句"); got != "短句" {
		t.Fatalf("short string should not be truncated: %q", got)
	}
}
