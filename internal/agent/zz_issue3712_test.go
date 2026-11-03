package agent

import "testing"

func TestIssue3712ChineseLiftReleases(t *testing.T) {
	l := newRefusalLedger(t.TempDir())
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)

	cases := []struct {
		name string
		text string
		want int
	}{

		{"english baseline", "go ahead, edit internal/auth/core.go", 1},

		{"trailing bare jixu", "go ahead, edit internal/auth/core.go，继续", 1},

		{"pure chinese strong lift", "可以了，改吧，继续，处理 internal/auth/core.go", 1},

		{"allow word lifts", "允许修改 internal/auth/core.go 了", 1},
		{"release word lifts", "解除限制，可以动 internal/auth/core.go", 1},

		{"reaffirm jixu biepeng", "ok，继续别碰 internal/auth/core.go", 0},
		{"reaffirm baochi xianzhuang", "可以了？不，保持现状，别碰 internal/auth/core.go", 0},
		{"reaffirm don't touch", "ok, don't touch internal/auth/core.go, that's right", 0},

		{"no lift phrase", "看看 internal/auth/core.go 就好", 0},
	}
	for _, tc := range cases {
		if got := l.release(tc.text); got != tc.want {
			t.Errorf("%s: release(%q) = %d, want %d", tc.name, tc.text, got, tc.want)
		}

		if tc.want == 1 {
			l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)
		}
	}
}

func TestIssue3712PatternLevelCJKMatching(t *testing.T) {

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

	if refusalReaffirmPattern.MatchString("，继续") {
		t.Error("bare 「继续」 still vetoes - defect A not fixed")
	}

	for _, s := range []string{"继续别碰", "继续不要动", "保持现状", "保持不动"} {
		if !refusalReaffirmPattern.MatchString(s) {
			t.Errorf("reaffirm compound %q no longer matches - #3469 regression", s)
		}
	}
}
