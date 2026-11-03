package agent

import (
	"testing"
	"unicode/utf8"
)

func TestIssue4015StripMarkerOffsetStaysOnOriginalString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{

			name: "growth char ɐ before marker",
			in:   "注意 ɐ done <GOAL_ACHIEVED/> Shipping complete.",
			want: "Shipping complete.",
		},
		{

			name: "shrink char ſ before marker",
			in:   "ļ ſ ok <GOAL_ACHIEVED/> Done, all tests green.",
			want: "Done, all tests green.",
		},
		{

			name: "shrink char ı before marker",
			in:   "ıstanbul check <GOAL_ACHIEVED/> 登录页修复完成",
			want: "登录页修复完成",
		},
		{

			name: "no-drift char ß still strips correctly",
			in:   "Straße fertig. <GOAL_ACHIEVED/> 登录页修复完成",
			want: "登录页修复完成",
		},
		{

			name: "lowercase marker still stripped",
			in:   "done with everything <goal_achieved/> ship it",
			want: "ship it",
		},
		{
			name: "marker at start",
			in:   "<GOAL_ACHIEVED/> shipped",
			want: "shipped",
		},
		{

			name: "no marker passthrough",
			in:   "do NOT emit the GOAL_ACHIEVED tag yet, more work remains",
			want: "do NOT emit the GOAL_ACHIEVED tag yet, more work remains",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripStrategistCompleteMarker(tc.in); got != tc.want {
				t.Fatalf("stripStrategistCompleteMarker(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIssue4015StripMarkerResultIsValidUTF8(t *testing.T) {
	in := "ɐɐɐ <GOAL_ACHIEVED/> 完成了所有目标。"
	got := stripStrategistCompleteMarker(in)
	if got != "完成了所有目标。" {
		t.Errorf("utf8 case: got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
}
