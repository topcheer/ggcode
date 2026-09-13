package knight

// #1825 case 3 regression: a MIXED CJK+ASCII word ("好v2") sanitized to
// "v2" (<8, rejected as a candidate) while cjkOnly=false kept it out of
// the hash fallback - pure-CJK and pure-ASCII words both survived but
// mixed ones fell through the gap and were silently dropped.

import (
	"strings"
	"testing"
)

func TestCorrectionNameMixedCJKWord(t *testing.T) {
	// Lone mixed word: the join is "v2" (<8, invalid) and hasCJK must
	// route it to the hash fallback instead of dropping it.
	got := buildCorrectionSkillName("好v2")
	if !strings.HasPrefix(got, "cjk-correction-") {
		t.Fatalf("lone mixed word must reach the hash fallback, got %q", got)
	}
	// Mixed word embedded with enough ASCII neighbors: the >=8 join is a
	// VALID candidate and must pass through (not hashed).
	if got := buildCorrectionSkillName("fix 好v2 crash"); got != "fix-v2-crash" {
		t.Fatalf("valid join with mixed word must pass through, got %q", got)
	}
	// Pure CJK unchanged.
	if got := buildCorrectionSkillName("修复 好了"); !strings.HasPrefix(got, "cjk-correction-") {
		t.Fatalf("pure CJK must keep the hash fallback, got %q", got)
	}
	// Pure-ASCII short join still returns the join (upstream candidate
	// validation rejects it) - unchanged from before, and NOT hashed.
	if got := buildCorrectionSkillName("abc def"); got != "abc-def" {
		t.Fatalf("pure-ASCII short join must stay a plain join, got %q", got)
	}
	// Long valid ASCII join passes through.
	if got := buildCorrectionSkillName("memoryleak detector"); !strings.Contains(got, "memoryleak") {
		t.Fatalf("valid ASCII join must pass through, got %q", got)
	}
}
