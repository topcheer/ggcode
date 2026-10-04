package agent

// Regression probes for #3257: the three byte-slice truncation sites in
// traj_intel.go cut UTF-8 sequences mid-rune on CJK text (~2/3 hit rate
// for pure CJK), and json.Encoder silently replaced the dangling bytes
// with U+FFFD - permanent corruption in the persisted learnings store and
// every re-injected system prompt. The fix routes all three sites through
// a shared rune-safe truncateRunesUTF8 and corrects the truncation
// marker's budget reservation (was 20, the marker is 34 bytes).

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestIssue3257_TruncateTaskCJKNoInvalidUTF8(t *testing.T) {
	// The issue's exact reproducer shape: ASCII prefix + CJK overflow.
	s := "a" + strings.Repeat("请", 130)
	for _, max := range []int{120, 100, 37, 36, 4, 3, 2, 1} {
		got := truncateTask(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d: truncateTask produced invalid UTF-8: %q", max, got)
		}
		if len(got) > max {
			t.Fatalf("max=%d: result exceeds budget: %d bytes", max, len(got))
		}
	}
	// Short input passes through untouched.
	if got := truncateTask("short", 120); got != "short" {
		t.Fatalf("short input mutated: %q", got)
	}
}

func TestIssue3257_SummarizeErrorsCJKSafe(t *testing.T) {
	long := strings.Repeat("错", 80) // 240 bytes > 100
	got := summarizeErrors([]string{long})
	if !utf8.ValidString(got) {
		t.Fatalf("summarizeErrors produced invalid UTF-8: %q", got)
	}
	if len(got) > 103 { // 100 cap + "..."
		t.Fatalf("summarizeErrors exceeds cap: %d", len(got))
	}
}

func TestIssue3257_RenderSectionBudgetHonoredAndCJKSafe(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	var ls []trajectoryLearning
	// per-Type cap is 3, so three fat entries must alone exceed the
	// 1200-byte budget to force the truncation path.
	for i := 0; i < 8; i++ {
		ls = append(ls, mkLearning(base.Add(-time.Duration(i)*time.Minute),
			"strategy", string(rune('a'+i)), strings.Repeat("策", 200)))
	}
	writeLearnings(t, dir, ls)
	s := &trajIntelState{}
	sec := s.RenderPromptSection(dir)
	if sec == "" {
		t.Fatal("section must render")
	}
	if !utf8.ValidString(sec) {
		t.Fatal("RenderPromptSection produced invalid UTF-8 after truncation")
	}
	if len(sec) > 1200 {
		t.Fatalf("section exceeds trajPromptMaxChars: %d bytes (legacy marker overshoot regression)", len(sec))
	}
	if !strings.Contains(sec, "older learnings truncated") {
		t.Fatal("truncation marker must be present when the budget is hit")
	}
}

func TestIssue3257_TruncateRunesUTF8Boundaries(t *testing.T) {
	if got := truncateRunesUTF8("abc", 5); got != "abc" {
		t.Fatalf("under budget passthrough: %q", got)
	}
	// Exactly at budget: untouched, no ellipsis.
	if got := truncateRunesUTF8("abcdef", 6); got != "abcdef" {
		t.Fatalf("exact budget: %q", got)
	}
	// Tiny budget shorter than the ellipsis: rune-safe head only.
	got := truncateRunesUTF8("请你好", 4)
	if !utf8.ValidString(got) || len(got) > 4 {
		t.Fatalf("tiny budget: %q (valid=%v)", got, utf8.ValidString(got))
	}
}
