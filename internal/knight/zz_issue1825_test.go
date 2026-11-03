package knight

import (
	"strings"
	"testing"
)

func TestCorrectionNameMixedCJKWord(t *testing.T) {

	got := buildCorrectionSkillName("好v2")
	if !strings.HasPrefix(got, "cjk-correction-") {
		t.Fatalf("lone mixed word must reach the hash fallback, got %q", got)
	}

	if got := buildCorrectionSkillName("fix 好v2 crash"); got != "fix-v2-crash" {
		t.Fatalf("valid join with mixed word must pass through, got %q", got)
	}

	if got := buildCorrectionSkillName("修复 好了"); !strings.HasPrefix(got, "cjk-correction-") {
		t.Fatalf("pure CJK must keep the hash fallback, got %q", got)
	}

	if got := buildCorrectionSkillName("abc def"); got != "abc-def" {
		t.Fatalf("pure-ASCII short join must stay a plain join, got %q", got)
	}

	if got := buildCorrectionSkillName("memoryleak detector"); !strings.Contains(got, "memoryleak") {
		t.Fatalf("valid ASCII join must pass through, got %q", got)
	}
}
