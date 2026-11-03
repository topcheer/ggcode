package agent

import (
	"strings"
	"testing"
)

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

	if !strings.Contains(got, ":12:") {
		t.Fatalf("report must reference B's line-12 instance, not re-report A's line 6:\n%s", got)
	}
	if strings.Contains(got, ":6:") {
		t.Fatalf("pre-existing A instance must stay suppressed:\n%s", got)
	}
}

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
