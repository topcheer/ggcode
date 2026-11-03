package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIssue4229HasPlanIntentWordBoundary(t *testing.T) {
	negative := []string{
		"explanation of the failure",
		"the plant is in the corner",
		"auto download the artifact",
		"footsteps in the log",
		"the roadmap-external noise",
	}
	for _, text := range negative[:4] {
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

func TestIssue4229TruncateRuneSafe(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"abcdefg", 6, "abc..."},
		{"你好世界", 8, "你..."},
		{"你好世界", 6, "你..."},
		{"ab你好", 5, "ab..."},
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

	long := strings.Repeat("界", 20)
	for max := 1; max <= len(long); max++ {
		if got := truncate(long, max); !utf8.ValidString(got) {
			t.Fatalf("truncate(repeated CJK, %d) produced invalid UTF-8: %q", max, got)
		}
	}
}
