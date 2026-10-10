package agent

// zz_issue3712_test.go pins the #3712 fix: refusal ledger release() failed
// 100% for pure-Chinese lift phrases because the three lift patterns kept
// their CJK words inside ASCII \b(...) groups (Go \b never matches beside
// CJK runes), and the reaffirm guard's bare 「继续」/「保持」 unconditionally
// vetoed any release that happened to end with "continue" - including
// "go ahead, edit config.yaml，继续" (English lift + Chinese trailing
// "continue" that reaffirms nothing).
//
// Fix: CJK branches live outside \b groups (mirroring what
// refusalReaffirmPattern's own comment already documented), and the bare
// 继续/保持 are now compound forms (继续别/继续不.../保持现状/保持不动)
// so "keep not touching" reaffirms while a trailing bare 继续 does not.

import "testing"

func TestIssue3712ChineseLiftReleases(t *testing.T) {
	l := newRefusalLedger(t.TempDir())
	l.record("don't touch internal/auth/core.go", "nl")

	cases := []struct {
		name string
		text string
		want int
	}{
		// English baseline must not regress (#3469 semantics).
		{"english baseline", "go ahead, edit internal/auth/core.go", 1},
		// Defect A: a trailing bare 「继续」 ("continue") is not a
		// reaffirmation - the English lift + target must still release.
		{"trailing bare jixu", "go ahead, edit internal/auth/core.go，继续", 1},
		// Defect A+B combined: pure-Chinese strong lift (可以了) plus a
		// trailing bare 继续. Before the fix: lift word dead inside \b AND
		// bare 继续 vetoed - released nothing.
		{"pure chinese strong lift", "可以了，改吧，继续，处理 internal/auth/core.go", 1},
		// Defect B: CJK strong lift words now fire outside the \b group.
		{"allow word lifts", "允许修改 internal/auth/core.go 了", 1},
		{"release word lifts", "解除限制，可以动 internal/auth/core.go", 1},
		// Reaffirm compounds must keep vetoing (#3469 anti-regression):
		// 「继续别碰」 and 「保持现状」 are genuine re-affirmations.
		{"reaffirm jixu biepeng", "ok，继续别碰 internal/auth/core.go", 0},
		{"reaffirm baochi xianzhuang", "可以了？不，保持现状，别碰 internal/auth/core.go", 0},
		{"reaffirm don't touch", "ok, don't touch internal/auth/core.go, that's right", 0},
		// No lift phrase at all: still a no-op release.
		{"no lift phrase", "看看 internal/auth/core.go 就好", 0},
	}
	for _, tc := range cases {
		if got := l.release(tc.text); got != tc.want {
			t.Errorf("%s: release(%q) = %d, want %d", tc.name, tc.text, got, tc.want)
		}
		// Re-record so each case starts from one live entry.
		if tc.want == 1 {
			l.record("don't touch internal/auth/core.go", "nl")
		}
	}
}

func TestIssue3712PatternLevelCJKMatching(t *testing.T) {
	// Direct pattern-level pins for the \b escape: each CJK lift word must
	// match in a purely Chinese context (no ASCII neighbors to lean \b on).
	strongCases := []string{"可以了，就这样", "允许他修改", "解除封锁"}
	for _, s := range strongCases {
		if !refusalStrongLiftPattern.MatchString(s) {
			t.Errorf("refusalStrongLiftPattern failed to match CJK lift %q", s)
		}
	}
	authCases := []string{"可以动它", "已经改了", "去碰那个文件"}
	for _, s := range authCases {
		if !refusalAuthorizePattern.MatchString(s) {
			t.Errorf("refusalAuthorizePattern failed to match CJK auth %q", s)
		}
	}
	if !refusalReleasePattern.MatchString("可以了，改吧") {
		t.Error("refusalReleasePattern failed to match CJK release phrase")
	}
	// Bare 继续 alone must no longer be a reaffirm veto...
	if refusalReaffirmPattern.MatchString("，继续") {
		t.Error("bare 「继续」 still vetoes - defect A not fixed")
	}
	// ...but the compound reaffirmations must.
	for _, s := range []string{"继续别碰", "继续不要动", "保持现状", "保持不动"} {
		if !refusalReaffirmPattern.MatchString(s) {
			t.Errorf("reaffirm compound %q no longer matches - #3469 regression", s)
		}
	}
}
