package tool

import (
	"testing"
)

// #3145 probes: rg content lines must be deterministically ordered by
// (path, lineNum) before the offset/head_limit window is cut, matching
// the fallback scanner - otherwise pagination skips/duplicates lines
// across calls (rg's parallel output order is unstable).

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
		"a.go:12:dup-line-same-num-stable", // stable within equal keys
		"b.go:2:boo",
		"b.go:7:bee",
	}
	for i := range want {
		if in[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (full: %v)", i, in[i], want[i], in)
		}
	}
}

// Continuation/context lines without a parseable prefix keep their
// relative order after their anchor (stable sort, unparseable last).
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

// context lines with '-' separator (rg -A/-B context) also parse.
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

// The pagination window itself: offset slicing over the sorted lines is
// now isomorphic with the fallback path's sorted slice (spot check via
// the key parser on realistic rg output).
func TestIssue3145_WindowIsomorphism(t *testing.T) {
	// Two "calls" with rg emitting files in different orders; the SAME
	// offset window must select the same lines after sorting.
	call1 := []string{"a.go:1:x", "a.go:2:y", "b.go:1:z"}
	call2 := []string{"b.go:1:z", "a.go:2:y", "a.go:1:x"}
	sortRgContentLines(call1)
	sortRgContentLines(call2)
	// window [1:3] on both
	w1 := call1[1:3]
	w2 := call2[1:3]
	for i := range w1 {
		if w1[i] != w2[i] {
			t.Fatalf("window diverged: %v vs %v", w1, w2)
		}
	}
}
