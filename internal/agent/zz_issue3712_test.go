package agent

// #3712: pure-Chinese refusal release was 100% dead - the three lift
// patterns kept their CJK terms inside \b(...) groups (Go regexp \b is an
// ASCII word boundary that never matches beside CJK runes), and the bare
// word 继续 in refusalReaffirmPattern unconditionally vetoed every Chinese
// release sentence ending in "继续" (English "continue" was never on the
// list). Matrix mirrors the issue's isolated E/F/G reproduction.

import "testing"

func TestRefusalReleaseCJK(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		// E: English lift + trailing 继续 must not be swallowed anymore.
		{"english lift plus bare continue", "go ahead, edit config.yaml，继续", 1},
		// Pure-Chinese strong lift (可以了 hoisted out of the \b group) with
		// target mention and trailing 继续.
		{"chinese strong lift with continue tail", "可以了，改 config.yaml，继续", 1},
		// Weak English prefix + CJK authorize word (可以 hoisted) + target.
		{"weak prefix plus cjk authorize", "ok，可以编辑 config.yaml 了", 1},
		// Pure-Chinese strong lift via 解除.
		{"chinese explicit release verb", "解除限制，可以改 config.yaml", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newRefusalTestLedger(t)
			l.record("don't touch config.yaml", "nl")
			if n := l.release(tc.text); n != tc.want {
				t.Fatalf("release(%q) = %d, want %d", tc.text, n, tc.want)
			}
		})
	}
}

// Reaffirmations in Chinese must still fail closed - the #3712 fix narrowed
// the bare 继续/保持 veto words to compounds, not removed the veto.
func TestRefusalReleaseCJKReaffirmStillFailsClosed(t *testing.T) {
	cases := []string{
		// G row: reaffirm intent - lift opener, then continues the refusal.
		"可以了，改吧，继续别碰 config.yaml",
		"ok，继续不要碰 config.yaml",
		"保持现状，config.yaml 别动",
	}
	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			l := newRefusalTestLedger(t)
			l.record("don't touch config.yaml", "nl")
			if n := l.release(text); n != 0 {
				t.Fatalf("reaffirmation released %d entries: %q", n, text)
			}
		})
	}
}

// Authorize-only Chinese sentence stays a no-op by design (the weak/strong
// split from #3469 requires a lift opener; 可以 alone is not one) - the
// difference vs. pre-#3712 is that pairing it with ANY lift opener now
// works, which the weak-prefix case above proves.
func TestRefusalReleaseCJKAuthorizeOnlyNoop(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch config.yaml", "nl")
	if n := l.release("可以编辑 config.yaml 了"); n != 0 {
		t.Fatalf("authorize-only released %d, want 0 (weak/strong policy)", n)
	}
}
