package agent

import (
	"strings"
	"testing"
)

// #1017 regression tests: instance-based delta (not count comparison) and
// nested-loop dedup.

// Equal-count pattern swap must now be reported: OLD1/OLD2 replaced by the
// same number of NEW patterns — count comparison suppressed this entirely.
func TestRegexLoopEqualCountSwapReported(t *testing.T) {
	oldSrc := `package p
import "regexp"
func f() {
	for i := 0; i < 2; i++ {
		_ = regexp.MustCompile("OLD1-[0-9]+")
		_ = regexp.MustCompile("OLD2-[a-z]+")
	}
}`
	newSrc := `package p
import "regexp"
func f() {
	for i := 0; i < 2; i++ {
		_ = regexp.MustCompile("NEW1-[0-9]+")
		_ = regexp.MustCompile("NEW2-[a-z]+")
	}
}`
	warns := checkRegexLoop("x.go", oldSrc, newSrc)
	if len(warns) == 0 {
		t.Fatal("equal-count pattern swap must be reported (under-report bug #1017)")
	}
	if !strings.Contains(warns[0], "NEW1") {
		t.Logf("warnings: %v", warns)
	}
}

// Adding one instance must only report the NEW pattern, not re-flag the
// untouched old instance (over-report bug #1017).
func TestRegexLoopDeltaReportsOnlyNewInstance(t *testing.T) {
	oldSrc := `package p
import "regexp"
func f() {
	for i := 0; i < 2; i++ {
		_ = regexp.MustCompile("SAME-[0-9]+")
	}
}`
	newSrc := `package p
import "regexp"
func f() {
	for i := 0; i < 2; i++ {
		_ = regexp.MustCompile("SAME-[0-9]+")
		_ = regexp.MustCompile("ADDED-[a-z]+")
	}
}`
	warns := checkRegexLoop("x.go", oldSrc, newSrc)
	if len(warns) == 0 {
		t.Fatal("new ADDED pattern must be reported")
	}
	for _, w := range warns {
		if strings.Contains(w, "SAME-") && strings.Contains(w, "inside loop") {
			t.Fatalf("untouched SAME- instance re-reported (over-report bug #1017): %s", w)
		}
	}
}

// A nested loop must count each compile call once.
func TestRegexLoopNestedNoDoubleCount(t *testing.T) {
	src := `package p
import "regexp"
func f(xs []string) {
	for _, x := range xs {
		for _, y := range xs {
			_ = regexp.MustCompile("NEST-[0-9]+" + x + y)
			_ = y
		}
	}
}`
	issues := findRegexInLoops("x.go", src)
	if len(issues) != 1 {
		t.Fatalf("nested loop must yield exactly 1 issue, got %d (double-count bug #1017)", len(issues))
	}
}

// Old nested (double-counted as 2 by the old code) vs new single-level:
// the new pattern must be reported despite old raw count >= new raw count.
func TestRegexLoopNestedPollutionFixed(t *testing.T) {
	oldSrc := `package p
import "regexp"
func f(xs []string) {
	for _, x := range xs {
		for range xs {
			_ = regexp.MustCompile("OLD-[0-9]+" + x)
		}
	}
}`
	newSrc := `package p
import "regexp"
func f(xs []string) {
	for _, x := range xs {
		_ = regexp.MustCompile("FRESH-[0-9]+" + x)
	}
}`
	warns := checkRegexLoop("x.go", oldSrc, newSrc)
	if len(warns) == 0 {
		t.Fatal("FRESH pattern suppressed by polluted old counts (delta bug #1017)")
	}
}
