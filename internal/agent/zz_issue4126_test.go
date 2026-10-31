package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// #4126: a LONG first line (>=100 runes) opening with a strong rejection or
// frustration word is still course-correction feedback. Previously the isShort
// gate swallowed it AND analyzeAndUpdate reset consecutiveNegatives, so two
// detailed corrections in a row restarted the escalation ladder at level 1.

func longCJKWithOpener(opener string) string {
	filler := strings.Repeat("请按照我下面描述的顺序重新处理每一个步骤并且核对输出，", 6)
	msg := opener + "，这里你理解错了，" + filler
	if utf8.RuneCountInString(msg) < 100 {
		panic("test message must be >=100 runes, got " + string(rune('0'+utf8.RuneCountInString(msg))))
	}
	return msg
}

func TestIssue4126_LongRejectionOpenerDetected(t *testing.T) {
	msg := longCJKWithOpener("不对")
	if got := detectNegativeFeedback(msg); got != negCatRejection {
		t.Errorf("long CJK rejection opener = %q, want %q", got, negCatRejection)
	}

	long := "wrong approach entirely - " + strings.Repeat("what I actually asked for was the buffered path ", 3)
	if utf8.RuneCountInString(long) < 100 {
		t.Fatalf("English case must be >=100 runes, got %d", utf8.RuneCountInString(long))
	}
	if got := detectNegativeFeedback(long); got != negCatRejection {
		t.Errorf("long EN rejection opener = %q, want %q", got, negCatRejection)
	}
}

func TestIssue4126_LongFrustrationOpenerDetected(t *testing.T) {
	filler := strings.Repeat("请你仔细看我写的每一条需求描述然后再改，", 6)
	msg := "烦死了，" + filler
	if utf8.RuneCountInString(msg) < 100 {
		t.Fatalf("must be >=100 runes, got %d", utf8.RuneCountInString(msg))
	}
	if got := detectNegativeFeedback(msg); got != negCatFrustration {
		t.Errorf("long CJK frustration opener = %q, want %q", got, negCatFrustration)
	}
}

func TestIssue4126_EscalationNotResetByLongDetailedCorrection(t *testing.T) {
	s := newUserSentimentState()
	if fb := s.analyzeAndUpdate("不对"); fb.Level != 1 {
		t.Fatalf("first correction level = %d, want 1", fb.Level)
	}
	if fb := s.analyzeAndUpdate(longCJKWithOpener("不对")); fb.Level != 2 {
		t.Fatalf("long detailed correction must escalate to 2, got %d (counter was reset)", fb.Level)
	}
	if fb := s.analyzeAndUpdate("又错了"); fb.Level != 3 {
		t.Fatalf("third correction level = %d, want 3", fb.Level)
	}
}

// Negative control: a rejection word buried mid-sentence in a long message
// (past the 40-rune opener window) stays context, not feedback.
func TestIssue4126_LongMidSentenceWordStillIgnored(t *testing.T) {
	filler := strings.Repeat("the migration plan covers schema changes and backfills ", 3)
	msg := filler + "and notes the old index was broken during rollout"
	if utf8.RuneCountInString(msg) < 100 {
		t.Fatalf("must be >=100 runes, got %d", utf8.RuneCountInString(msg))
	}
	if got := detectNegativeFeedback(msg); got != "" {
		t.Errorf("mid-sentence word in long message = %q, want empty", got)
	}
}
