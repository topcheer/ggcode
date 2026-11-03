package agent

import (
	"strings"
	"testing"
)

func cjkFiller(n int) string { return strings.Repeat("这", n) }

func TestIssue3968CJKLongRejectionDetected(t *testing.T) {
	msg := cjkFiller(20) + "还是不对" + cjkFiller(16)
	if got := detectNegativeFeedback(msg); got != negCatRejection {
		t.Fatalf("40-rune CJK message with 还是不对: category=%q, want %q (byte-length gate skipped it before #3968)", got, negCatRejection)
	}
}

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

func TestIssue3968CJKRedirectionOpenerWindow(t *testing.T) {
	msg := cjkFiller(20) + "重新来" + cjkFiller(100)
	if got := detectNegativeFeedback(msg); got != negCatRedirection {
		t.Fatalf("CJK redirection opener at rune 20 (byte 60): category=%q, want %q", got, negCatRedirection)
	}
}

func TestIssue3968EnglishLongUnchanged(t *testing.T) {
	msg := strings.Repeat("x", 120) + " wrong"
	if got := detectNegativeFeedback(msg); got != "" {
		t.Fatalf("long English message with bare 'wrong': category=%q, want empty (gate semantics must not widen for ASCII)", got)
	}

	if got := detectNegativeFeedback("that's wrong"); got != negCatRejection {
		t.Fatalf("short English rejection: category=%q, want %q", got, negCatRejection)
	}
}
