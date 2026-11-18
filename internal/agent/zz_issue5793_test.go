package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #5793: scope_creep excerpt windows used byte arithmetic and a second
// byte-level cut, producing invalid UTF-8 on CJK-mixed text (same family
// as #5775/#5776). The whole excerpt chain must stay rune-safe.
func Test5793ScopeCreepExcerptCJKValidUTF8(t *testing.T) {
	// Pattern-matching phrase (hits scopeCreepPatterns) surrounded by CJK
	// runs: ±20/+40 byte window lands mid-rune and excerpt exceeds 80 runes.
	cjk := strings.Repeat("持续扩展鉴权范围模块", 30) // 300 runes of 3-byte runes
	text := "前段 " + cjk + " while I'm at it I'll also fix the imports and clean up 后段 " + cjk

	hits := scanScopeCreep(text)
	if len(hits) == 0 {
		t.Fatal("expected at least one scope-creep hit on pattern text")
	}
	for _, h := range hits {
		if !utf8.ValidString(h.excerpt) {
			t.Fatalf("excerpt contains invalid UTF-8: %q", h.excerpt)
		}
	}
}
