package knight

import "testing"

// Regression for #1622-A/B: Korean must tokenize (not 0 tokens) and
// full-width ASCII must differentiate versions.
func TestTokenizeCJKFullCoverage(t *testing.T) {
	ko := tokenizeForSimilarity("빌드 검증 자동화 스킬")
	if len(ko) == 0 {
		t.Fatal("Korean must produce tokens (dedup was dead)")
	}
	ja := tokenizeForSimilarity("ビルドを検証する")
	if len(ja) < 3 {
		t.Fatalf("Japanese kana must tokenize, got %d tokens", len(ja))
	}
	a := tokenizeForSimilarity("版本Ｖ１．２")
	b := tokenizeForSimilarity("版本")
	if len(a) == len(b) {
		t.Fatal("full-width ASCII must differentiate versioned names")
	}
}
