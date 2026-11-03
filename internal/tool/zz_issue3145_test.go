package tool

import (
	"testing"
)

func TestIssue3145_SortRgContentLinesDeterministic(t *testing.T) {
	in := []string{
		"b.go:7:bee",
		"a.go:12:zeta",
		"a.go:3:alpha",
		"b.go:2:boo",
		"a.go:12:dup-line-same-num-stable",
	}
	sortRgContentLines(in)
	want := []string{
		"a.go:3:alpha",
		"a.go:12:zeta",
		"a.go:12:dup-line-same-num-stable",
		"b.go:2:boo",
		"b.go:7:bee",
	}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (full: %v)", i, in[i], want[i], in)
		}
	}
}

func TestIssue3145_UnparseableLinesKeepRelativeOrder(t *testing.T) {
	in := []string{
		"z.go:1:top",
		"    continuation of z",
		"a.go:1:aaa",
		"    continuation of a",
	}
	sortRgContentLines(in)
	if in[0] != "a.go:1:aaa" || in[2] != "z.go:1:top" {
		t.Fatalf("parseable lines not ordered: %v", in)
	}
	if in[1] != "    continuation of a" || in[3] != "    continuation of z" {
		t.Fatalf("continuation lines detached from anchors: %v", in)
	}
}

func TestIssue3145_ContextDashLinesParse(t *testing.T) {
	in := []string{
		"m.go:5-middle",
		"m.go:2-top",
	}
	sortRgContentLines(in)
	if in[0] != "m.go:2-top" || in[1] != "m.go:5-middle" {
		t.Fatalf("dash-context ordering wrong: %v", in)
	}
}

func TestIssue3145_WindowIsomorphism(t *testing.T) {

	call1 := []string{"a.go:1:x", "a.go:2:y", "b.go:1:z"}
	call2 := []string{"b.go:1:z", "a.go:2:y", "a.go:1:x"}
	sortRgContentLines(call1)
	sortRgContentLines(call2)

	w1 := call1[1:3]
	w2 := call2[1:3]
	for i := range w1 {
		if w1[i] != w2[i] {
			t.Fatalf("window diverged: %v vs %v", w1, w2)
		}
	}
}
