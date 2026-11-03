package agent

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestIssue2881LocalVarNamedCheckNotAssertion(t *testing.T) {

	src := `package foo

import "testing"

func newTestChecker() *testChecker { return nil }
type testChecker struct{}
func (c *testChecker) record(v any) {}

func TestLocalCheck(t *testing.T) {
	check := newTestChecker()
	result := 1 + 1
	check.record(result)
}
`
	got := checkAssertionPresence("foo_test.go", "", src)
	if got == "" {
		t.Fatal("#2881: hollow test using a local variable named `check` escaped detection - check.record was misread as an assertion call")
	}
	if !strings.Contains(got, "TestLocalCheck") {
		t.Fatalf("expected TestLocalCheck to be flagged, got: %q", got)
	}
}

func TestIssue2881ImportedRequireStillCounts(t *testing.T) {

	src := `package foo

import (
	"testing"
	"github.com/stretchr/testify/require"
)

func TestRealRequire(t *testing.T) {
	require.Equal(t, 2, 1+1)
}
`
	if got := checkAssertionPresence("foo_test.go", "", src); got != "" {
		t.Fatalf("test with imported require.Equal must not be flagged hollow, got: %q", got)
	}
}

func TestIssue2881AliasedImportCounts(t *testing.T) {

	src := `package foo

import (
	"testing"
	check "gopkg.in/check.v1"
)

func TestAliasedCheck(t *testing.T) {
	_ = check
	_ = testing.Verbose
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "foo_test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := assertionPkgQualifiers(file)
	if !q["check"] {
		t.Fatal("#2881: explicit alias `check` for gopkg.in/check.v1 must be recognized as an assertion-package qualifier")
	}
}
