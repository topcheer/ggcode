package agent

import "testing"

// #1488 case C(a) pin: alias-imported regexp compiles in loops must be detected.
func Test1488RegexAliasImportDetected(t *testing.T) {
	src := `package p
import r "regexp"
func f(items []string) bool {
	for _, s := range items {
		re := r.MustCompile("^a")
		if re.MatchString(s) { return true }
	}
	return false
}
`
	warns := findRegexInLoops("alias.go", src)
	if len(warns) == 0 {
		t.Fatal("alias import (import r \"regexp\") compile-in-loop undetected")
	}
	// Canonical name recorded.
	if warns[0].funcName != "regexp.MustCompile" {
		t.Fatalf("funcName should be canonical, got %q", warns[0].funcName)
	}
}

// #1488 case C(b) pin: growing 1 -> 2 instances of the same pattern must flag the surplus.
func Test1488RegexSamePatternGrowth(t *testing.T) {
	one := `package p
import "regexp"
func f(xs, ys []string) {
	for _, s := range xs {
		_ = regexp.MustCompile("^a").MatchString(s)
	}
}
`
	two := `package p
import "regexp"
func f(xs, ys []string) {
	for _, s := range xs {
		_ = regexp.MustCompile("^a").MatchString(s)
	}
	for _, s := range ys {
		_ = regexp.MustCompile("^a").MatchString(s)
	}
}
`
	if w := checkRegexLoop("g.go", one, two); len(w) == 0 {
		t.Fatal("added second instance of an existing pattern must be flagged (set-delta suppressed it)")
	}
	// Unchanged file stays silent.
	if w := checkRegexLoop("g.go", one, one); len(w) != 0 {
		t.Fatalf("no-delta edit must stay silent, got %v", w)
	}
}
