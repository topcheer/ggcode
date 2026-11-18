package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #5776 residual: the second cut excerpt[:80]+"..." was still a byte slice.
// An 80-CJK-rune excerpt is ~240 bytes, so excerpt[:80] cut mid-rune again.
// The fix keeps the whole chain rune-safe; this test pins exactly that path.
func Test5776ResidualSecondCutCJKValidUTF8(t *testing.T) {
	// English pattern phrase (matches prematureAbstrPatterns) surrounded by
	// CJK runs so the ±20/+40 byte window lands mid-rune and the excerpt
	// exceeds 80 runes.
	cjk := strings.Repeat("重构鉴权逻辑模块", 30) // 210 runes of 3-byte runes
	text := "前段 " + cjk + " I have created a factory pattern to solve this 后段 " + cjk

	hits := scanPrematureAbstraction(text)
	if len(hits) == 0 {
		t.Fatal("expected at least one premature-abstraction hit on CJK text")
	}
	for _, h := range hits {
		if !utf8.ValidString(h.excerpt) {
			t.Fatalf("excerpt contains invalid UTF-8: %q", h.excerpt)
		}
	}
}
