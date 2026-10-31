package agent

import (
	"strings"
	"testing"
)

// Issue #3968: detectNegativeFeedback used byte length for both the
// isShort gate and the redirection opener window. UTF-8 CJK runs 3 bytes
// per char, so every Chinese message over ~33 chars was misclassified as
// "long", silently skipping rejection/frustration hits and resetting the
// escalation counter mid-sequence - the exact failure #3739's CJK keyword
// tables were added to prevent.

// cjkFiller builds a Chinese filler string of n runes (3n bytes).
func cjkFiller(n int) string { return strings.Repeat("这", n) }

// 40 runes / 120 bytes: byte-length gate (>100) would skip it; the rune
// gate (<100) must catch the rejection keyword.
func TestIssue3968CJKLongRejectionDetected(t *testing.T) {
	msg := cjkFiller(20) + "还是不对" + cjkFiller(16) // 40 runes, 120 bytes
	if got := detectNegativeFeedback(msg); got != negCatRejection {
		t.Fatalf("40-rune CJK message with 还是不对: category=%q, want %q (byte-length gate skipped it before #3968)", got, negCatRejection)
	}
}

// Escalation must climb on consecutive long CJK criticisms. Pre-fix, the
// second message was classified non-negative and consecutiveNegatives was
// reset to 0, permanently pinning the level at 1.
func TestIssue3968CJKLongFeedbackEscalates(t *testing.T) {
	s := &userSentimentState{}
	first := s.analyzeAndUpdate(cjkFiller(20) + "还是不对" + cjkFiller(16))
	if first.Level != 1 {
		t.Fatalf("first long CJK criticism: level=%d, want 1", first.Level)
	}
	second := s.analyzeAndUpdate(cjkFiller(18) + "又失败了没成功" + cjkFiller(18))
	if second.Level != 2 {
		t.Fatalf("second consecutive long CJK criticism: level=%d, want 2 (byte gate reset the counter pre-fix)", second.Level)
	}
	if second.Category != negCatRejection {
		t.Fatalf("second criticism category=%q, want %q", second.Category, negCatRejection)
	}
}

// Redirection opener window: keyword at rune offset 20 lands at byte
// offset 60. Pre-fix the byte comparison (idx < 40) rejected it; the rune
// window must accept openers within the first 40 characters.
func TestIssue3968CJKRedirectionOpenerWindow(t *testing.T) {
	msg := cjkFiller(20) + "重新来" + cjkFiller(100) // 123 runes → isShort=false → opener path
	if got := detectNegativeFeedback(msg); got != negCatRedirection {
		t.Fatalf("CJK redirection opener at rune 20 (byte 60): category=%q, want %q", got, negCatRedirection)
	}
}

// English behavior must be unchanged: a genuinely long ASCII message with
// a bare rejection word stays excluded (byte index == rune count for
// ASCII, so the gate is identical).
func TestIssue3968EnglishLongUnchanged(t *testing.T) {
	msg := strings.Repeat("x", 120) + " wrong"
	if got := detectNegativeFeedback(msg); got != "" {
		t.Fatalf("long English message with bare 'wrong': category=%q, want empty (gate semantics must not widen for ASCII)", got)
	}
	// Short English rejection still fires (baseline, not a regression pin).
	if got := detectNegativeFeedback("that's wrong"); got != negCatRejection {
		t.Fatalf("short English rejection: category=%q, want %q", got, negCatRejection)
	}
}
