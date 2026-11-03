package knight

import "testing"

func TestTokenSetCJK_1582(t *testing.T) {
	set := tokenSet("配置重复检测")
	if len(set) < 3 {
		t.Fatalf("CJK text must yield per-rune tokens, got %d (%v)", len(set), set)
	}

	j := jaccardSimilarity(tokenSet("配置重复检测的技能描述文本"), tokenSet("配置重复检测技能的描述文本"))
	if j < 0.75 {
		t.Fatalf("rewritten CJK near-duplicates must score >=0.75, got %.2f", j)
	}
}
