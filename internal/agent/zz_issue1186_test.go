package agent

import (
	"strings"
	"testing"
)

// #1186: the #1179 suffix fallback must be a multiset (one token per old
// finding), not a set. Old fn A already contains one unguarded `s.Body`
// instance; adding fn B with the same shape is a genuinely NEW instance and
// must be reported instead of being silently absorbed by A's suffix entry.
func TestNilDerefSuffixMultisetCrossFunctionReport(t *testing.T) {
	old := `package p

func A() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	new := `package p

func A() {
	s, err := f()
	_ = err
	_ = s.Body
}

func B() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	got := checkNilDerefAfterError("x.go", old, new)
	if got == "" {
		t.Fatal("same-shape instance in NEW function B must be reported (was silently absorbed by suffix set)")
	}
	// The report anchors by file:line:var. A's instance sits at line 6 (its
	// token consumed), B's `_ = s.Body` deref at line 12 must be the one reported.
	if !strings.Contains(got, ":12:") {
		t.Fatalf("report must reference B's line-12 instance, not re-report A's line 6:\n%s", got)
	}
	if strings.Contains(got, ":6:") {
		t.Fatalf("pre-existing A instance must stay suppressed:\n%s", got)
	}
}

// Counter-test: without the A seed in the old content, B already reported -
// the fix must not regress plain new-instance reporting.
func TestNilDerefSuffixMultisetNewFunctionAlone(t *testing.T) {
	new := `package p

func B() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	if got := checkNilDerefAfterError("x.go", "", new); got == "" {
		t.Fatal("plain new instance must report")
	}
}

// A rename (A -> A2) keeps consuming exactly one old token (#1179 intent
// preserved): no re-report of the pre-existing instance.
func TestNilDerefSuffixRenameStillSuppressed(t *testing.T) {
	old := `package p

func A() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	new := `package p

func A2() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	if got := checkNilDerefAfterError("x.go", old, new); got != "" {
		t.Fatalf("renamed pre-existing instance must stay suppressed:\n%s", got)
	}
}

// Two old findings, two new same-shape findings: both consume a token, zero
// reported. A third new finding reports (surplus over old count).
func TestNilDerefSuffixMultisetExactCounts(t *testing.T) {
	old := `package p

func A() {
	s, err := f()
	_ = err
	_ = s.Body
}

func B() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	twoNew := `package p

func A() {
	s, err := f()
	_ = err
	_ = s.Body
}

func B() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	if got := checkNilDerefAfterError("x.go", old, twoNew); got != "" {
		t.Fatalf("unchanged instances must stay suppressed:\n%s", got)
	}

	threeNew := twoNew + `
func C() {
	s, err := f()
	_ = err
	_ = s.Body
}
`
	if got := checkNilDerefAfterError("x.go", old, threeNew); got == "" {
		t.Fatal("third same-shape instance has no token left and must report")
	}
}
