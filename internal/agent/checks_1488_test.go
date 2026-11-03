package agent

import "testing"

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

	if warns[0].funcName != "regexp.MustCompile" {
		t.Fatalf("funcName should be canonical, got %q", warns[0].funcName)
	}
}

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

	if w := checkRegexLoop("g.go", one, one); len(w) != 0 {
		t.Fatalf("no-delta edit must stay silent, got %v", w)
	}
}
