package agent

// zz_issue3739_cjk_sentiment_test.go -- companion tests for CJK negative
// feedback detection (#3739). Before the fix, pure-Chinese corrections
// ("错了" "停下" "别改了") matched no pattern, and such messages RESET the
// escalation counter accumulated by English negatives.

import "testing"

func TestIssue3739CJKRejection(t *testing.T) {
	cases := map[string]string{
		"错了":      negCatRejection,
		"不对，重新来":  negCatRedirection, // 不对/错了 vs 重新来 priority: rejection listed first
		"你改错了文件":  negCatRejection,
		"不行":      negCatRejection,
		"还是不对":    negCatRejection,
		"重新来":     negCatRedirection,
		"算了，换个方案": negCatRedirection,
		"停下":      negCatFrustration,
		"别改了":     negCatFrustration,
		"烦死了":     negCatFrustration,
	}
	// Note: "不对，重新来" hits rejection first (priority order), so fix expectation.
	cases["不对，重新来"] = negCatRejection
	for msg, want := range cases {
		if got := detectNegativeFeedback(msg); got != want {
			t.Errorf("detectNegativeFeedback(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestIssue3739CJKWhitelistStillPositive(t *testing.T) {
	positives := []string{
		"没问题", "谢谢，辛苦了", "好的，继续", "很好", "不错，就是这样", "完美",
	}
	for _, msg := range positives {
		if got := detectNegativeFeedback(msg); got != "" {
			t.Errorf("detectNegativeFeedback(%q) = %q, want empty (positive)", msg, got)
		}
	}
}

func TestIssue3739MixedLanguageNoCounterReset(t *testing.T) {
	// Scenario B from the issue: English negative then Chinese negative must
	// ESCALATE, not reset.
	s := &userSentimentState{}
	fb1 := s.analyzeAndUpdate("wrong file, revert it")
	if fb1.Level != 1 {
		t.Fatalf("first negative level = %d, want 1", fb1.Level)
	}
	fb2 := s.analyzeAndUpdate("不对")
	if fb2.Level != 2 {
		t.Fatalf("Chinese negative after English must escalate to 2, got %d (counter reset bug)", fb2.Level)
	}
	fb3 := s.analyzeAndUpdate("烦死了，停下")
	if fb3.Level != sentimentEscalationMax {
		t.Fatalf("third negative must reach max, got %d", fb3.Level)
	}
	if !shouldResetMonitoringOnFeedback(fb3) {
		t.Fatal("level >= strong must request monitoring reset")
	}
}

func TestIssue3739CJKLongMessageRedirectionOpener(t *testing.T) {
	// Long Chinese message: redirection fires only as an opener (<40 chars in).
	long := "算了，我们换个思路吧，因为刚才那个方案在这个代码库里行不通，我详细解释一下原因，首先……后面还有很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多很多"
	if got := detectNegativeFeedback(long); got != negCatRedirection {
		t.Errorf("long Chinese redirection opener = %q, want %q", got, negCatRedirection)
	}
}

func TestIssue3739PureChineseDoesNotResetEnglishCounters(t *testing.T) {
	// A NEUTRAL Chinese message (no negative signal) legitimately resets -
	// that is pre-existing designed behavior. What must NOT happen after the
	// fix is a Chinese NEGATIVE being misread as neutral.
	s := &userSentimentState{}
	s.analyzeAndUpdate("stop")
	fb := s.analyzeAndUpdate("别改了，先停下")
	if fb.Category != negCatFrustration || fb.Level != 2 {
		t.Fatalf("Chinese frustration after English stop: got %+v, want frustration level 2", fb)
	}
}
