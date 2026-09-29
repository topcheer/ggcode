package agent

import "testing"

// zz_issue2839_test.go - probe for #2839: pure-CJK subgoals yield zero
// keywords (the extractor regex is ASCII-only), so recordToolCall can never
// mark them addressed - counting them in maybeWarn guarantees a false
// skip-step warning. Subgoals without trackable keywords must be excluded
// from both numerator and denominator.
func TestIssue2839CJKPlanNoFalseWarning(t *testing.T) {
	// Verify the extraction premise: pure CJK desc yields no keywords.
	if kws := extractSubgoalKeywords("修复登录接口的鉴权逻辑并补充单元测试"); len(kws) != 0 {
		t.Logf("extraction premise changed: %v (guard below still applies)", kws)
	}

	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "修复登录接口的鉴权逻辑", keywords: nil},
		{number: 2, text: "补充单元测试覆盖分支", keywords: nil},
		{number: 3, text: "更新接口文档", keywords: nil},
	}
	s.planIter = 1
	if msg := s.maybeWarn(10); msg != "" {
		t.Errorf("#2839 pure-CJK plan fired false skip-step warning:\n%s", msg)
	}
}

func TestIssue2839MixedPlanOnlyTrackableCounted(t *testing.T) {
	// 2 CJK (untrackable) + 2 English: one English addressed, one not.
	// Old code: 3/4 unaddressed = 75% > 40% -> warn. New: 1/2 = 50% > 40% ->
	// still warns, but only for the genuinely trackable half. With both
	// English goals addressed, no warning despite CJK ones being "unaddressed".
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "修复登录接口", keywords: nil},
		{number: 2, text: "补充测试", keywords: nil},
		{number: 3, text: "update the config loader", keywords: []string{"config", "loader"}, addressed: true},
		{number: 4, text: "run the linter suite", keywords: []string{"linter", "suite"}, addressed: true},
	}
	s.planIter = 1
	if msg := s.maybeWarn(10); msg != "" {
		t.Errorf("#2839 untrackable subgoals leaked into warning:\n%s", msg)
	}

	// Genuinely skipped English subgoal still warns (no over-suppression).
	s2 := newSubgoalState()
	s2.subgoals = []subgoalEntry{
		{number: 1, text: "修复登录接口", keywords: nil},
		{number: 2, text: "补充测试", keywords: nil},
		{number: 3, text: "update the config loader", keywords: []string{"config", "loader"}, addressed: true},
		{number: 4, text: "run the linter suite", keywords: []string{"linter", "suite"}},
	}
	s2.planIter = 1
	if msg := s2.maybeWarn(10); msg == "" {
		t.Errorf("#2839 genuinely skipped trackable subgoal no longer warns (over-suppressed)")
	}
}

func TestIssue2839AllEnglishBehaviorUnchanged(t *testing.T) {
	// All-English plan, nothing addressed: warning still fires (base behavior).
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "fix auth handler", keywords: []string{"auth", "handler"}},
		{number: 2, text: "add unit tests", keywords: []string{"unit"}},
		{number: 3, text: "update docs", keywords: []string{"docs"}},
	}
	s.planIter = 1
	if msg := s.maybeWarn(10); msg == "" {
		t.Errorf("all-English unaddressed plan stopped warning (base behavior regressed)")
	}
}
