package agent

// #2881: goAssertionPkgs must match import-qualified identifiers, not bare
// identifier text. A local variable named check/should/quick used to credit
// any method call as an assertion, letting hollow tests escape detection.

import (
	"go/parser"
	"go/token"
	"testing"
)

// mfeSrc builds a *_test.go source whose TestFoo body contains no real
// assertion; header controls which import block is present.
func src2881(header string) string {
	return header + `

func TestFoo(t *testing.T) {
	check := newTestChecker()
	result := 1 + 1
	check.record(result)
}
`
}

func TestAssertionPresence_LocalCheckVarNotAssertion(t *testing.T) {
	// No assertion-package import: check is a local variable, so
	// check.record(result) must NOT count and TestFoo must be hollow.
	const src = `package x
import "testing"
`
	w := checkAssertionPresence("x_test.go", "", src2881(src))
	if w == "" {
		t.Fatal("TestFoo with only check.record(result) must be flagged hollow (local var, not pkg)")
	}
}

func TestAssertionPresence_ImportedCheckPkgCounts(t *testing.T) {
	// Real go-check import (gopkg.in/check.v1, version suffix must normalize):
	// check.record(...) is a package call and must count as non-hollow.
	const src = `package x
import "testing"
import "gopkg.in/check.v1"
`
	w := checkAssertionPresence("x_test.go", "", src2881(src))
	if w != "" {
		t.Fatalf("TestFoo with imported go-check must not be flagged, got %q", w)
	}
}

func TestAssertionPresence_AliasedRequireCounts(t *testing.T) {
	// Aliased testify import: r.Equal(...) must count.
	const src = `package x
import "testing"
import r "github.com/stretchr/testify/require"

func TestFoo(t *testing.T) {
	r.Equal(t, 2, 1+1)
}
`
	w := checkAssertionPresence("x_test.go", "", src)
	if w != "" {
		t.Fatalf("aliased require.Equal must count as assertion, got %q", w)
	}
}

func TestAssertionPresence_RequireVarWithoutImportIsHollow(t *testing.T) {
	// require.X called without any import (impossible in valid Go, but the
	// detector must not credit bare names it cannot see an import for).
	const src = `package x
import "testing"

func TestFoo(t *testing.T) {
	require.Equal(2, 1+1)
}
`
	w := checkAssertionPresence("x_test.go", "", src)
	if w == "" {
		t.Fatal("bare require.Equal without import must not be credited; test should be hollow-flagged")
	}
}

func TestAssertionImportQualifiers(t *testing.T) {
	src := `package x
import (
	"testing"
	"github.com/stretchr/testify/require"
	a2 "github.com/stretchr/testify/assert"
	"gopkg.in/check.v1"
	quick "testing/quick"
)
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	quals := assertionImportQualifiers(f)
	for _, want := range []string{"require", "a2", "check", "quick", "should"} {
		if !quals[want] {
			t.Errorf("qualifier %q missing, got %v", want, quals)
		}
	}
	for _, no := range []string{"assert", "testing", "x"} {
		if quals[no] {
			t.Errorf("qualifier %q must not be present, got %v", no, quals)
		}
	}
}

func TestTrimVersionSuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"check.v1", "check"},
		{"assert.v2", "assert"},
		{"require", "require"},
		{"my.pkg", "my.pkg"}, // no digits after .v
		{".v1", ".v1"},       // dot at index 0 -> unchanged
	} {
		if got := trimVersionSuffix(tc.in); got != tc.want {
			t.Errorf("trimVersionSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
