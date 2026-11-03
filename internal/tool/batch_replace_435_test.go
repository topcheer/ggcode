package tool

import (
	"regexp"
	"testing"
)

func TestInvalidGroupRefsBraced(t *testing.T) {
	re := regexp.MustCompile(`(a)(b)`)
	if got := invalidGroupRefs(re, "x${c}y"); got == "" {
		t.Error("unknown ${c} must be flagged")
	}
	if got := invalidGroupRefs(re, "x$cy"); got == "" {
		t.Error("unknown bare $c must still be flagged (regression)")
	}
	if got := invalidGroupRefs(re, "x${1}y"); got != "" {
		t.Errorf("valid ${1} must pass, got %q", got)
	}
	named := regexp.MustCompile(`(?P<first>a)(b)`)
	if got := invalidGroupRefs(named, "x${first}y"); got != "" {
		t.Errorf("known ${first} must pass, got %q", got)
	}
}
