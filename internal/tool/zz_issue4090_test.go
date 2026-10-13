package tool

// #4090: appendCriterion truncated criteria by raw bytes, cutting CJK text
// mid-rune and feeding invalid UTF-8 ("...\xe4\xb8...") into the acceptance
// reminder shown to the main LLM.
import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAppendCriterionUTF8Truncation(t *testing.T) {
	// 100 CJK chars = 300 bytes > maxAcceptanceItemLen(200); the old byte
	// slice c[:200] cut the 67th char mid-rune.
	cjk := strings.Repeat("验", 100)
	if len(cjk) <= maxAcceptanceItemLen {
		t.Fatalf("fixture too short: %d bytes", len(cjk))
	}
	got := appendCriterion(nil, cjk)
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1", len(got))
	}
	s := got[0]
	if !utf8.ValidString(s) {
		t.Errorf("appendCriterion produced invalid UTF-8: %q", s)
	}
	if !strings.HasPrefix(s, "验") {
		t.Errorf("criterion lost its prefix: %q", s[:12])
	}
}
