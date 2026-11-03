package knight

import "testing"

func dupOf(t *testing.T, existing, candidate string) bool {
	t.Helper()
	return CheckDuplicate(&SkillEntry{Name: "candidate-skill", Meta: SkillMeta{Description: candidate}},
		[]*SkillEntry{{Name: "existing-skill", Meta: SkillMeta{Description: existing}}})
}

func TestIssue1624ShortCJKOneCharDiffNotDuplicate(t *testing.T) {
	a := "自动格式化代码的命令行工具，可以快速处理"
	b := "自动格式化日志的命令行工具，可以快速处理"
	if dupOf(t, a, b) {
		t.Fatal("one-character CJK difference must not be a near-duplicate (0.85 threshold)")
	}
}

func TestIssue1624ExactCJKCopyStillDuplicate(t *testing.T) {
	a := "自动格式化代码的命令行工具，可以快速处理"
	if !dupOf(t, a, a) {
		t.Fatal("exact copy must still be flagged (J=1.0 > 0.85)")
	}
}

func TestIssue1624CjkRatio(t *testing.T) {
	if cjkRatio(tokenSet("自动格式化代码")) < 0.85 {
		t.Fatal("pure CJK description should be CJK-dominated")
	}
	if cjkRatio(tokenSet("format code automatically with gofmt tool")) > 0.1 {
		t.Fatal("ASCII word set should not be CJK-dominated")
	}
}
