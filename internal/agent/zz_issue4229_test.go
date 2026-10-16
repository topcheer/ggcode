package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestIssue4229HasPlanIntentWordBoundary guards the #4229 fix: plan-intent
// phrases must match on word boundaries, not as raw substrings, so
// "explanation", "plant" and "auto download" no longer announce plans.
func TestIssue4229HasPlanIntentWordBoundary(t *testing.T) {
	negative := []string{
		"explanation of the failure", // contains "plan"
		"the plant is in the corner", // contains "plan"
		"auto download the artifact", // contains "to do"
		"footsteps in the log",       // contains "steps"
		"the roadmap-external noise", // "roadmap" IS a word; see positive list
	}
	for _, text := range negative[:4] { // first four must NOT match
		if hasPlanIntent(text) {
			t.Errorf("hasPlanIntent(%q) = true, want false (substring false positive)", text)
		}
	}

	positive := []string{
		"Here is my plan: 1. fix auth 2. add tests 3. update docs",
		"Going to do three things now",
		"the roadmap for this task is",
		"我的计划：1. 修复认证 2. 更新文档",
		"接下来按步骤执行",
	}
	for _, text := range positive {
		if !hasPlanIntent(text) {
			t.Errorf("hasPlanIntent(%q) = false, want true", text)
		}
	}
}

// TestIssue4229TruncateRuneSafe guards the #4229 fix: truncation must never
// split a multi-byte rune, so the guidance message stays valid UTF-8.
func TestIssue4229TruncateRuneSafe(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},   // no truncation
		{"abcdefg", 6, "abc..."}, // ASCII cut
		{"你好世界", 8, "你..."},      // cut lands mid-rune, snaps back
		{"你好世界", 6, "你..."},      // cut exactly after rune 1
		{"ab你好", 5, "ab..."},     // mixed ASCII/CJK
	}
	for _, c := range cases {
		got := truncate(c.in, c.max)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q: invalid UTF-8", c.in, c.max, got)
		}
		if len(got) > c.max {
			t.Errorf("truncate(%q, %d) = %q: exceeds max", c.in, c.max, got)
		}
	}
	// Long CJK text stays valid UTF-8 at every threshold.
	long := strings.Repeat("界", 20)
	for max := 1; max <= len(long); max++ {
		if got := truncate(long, max); !utf8.ValidString(got) {
			t.Fatalf("truncate(repeated CJK, %d) produced invalid UTF-8: %q", max, got)
		}
	}
}
