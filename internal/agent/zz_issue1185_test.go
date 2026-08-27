package agent

import "testing"

// #1185: the nil-deref detector must model && short-circuit evaluation.
// `v != nil && v.Field != ""` is the canonical Go nil guard: the right
// conjunct only evaluates when v is non-nil, and the if body likewise.
// staticcheck SA5011 stays silent on this idiom; so must we.
func TestNilDerefLandShortCircuitGuardSuppressed(t *testing.T) {
	src := `package p

import "fmt"

func f() {
	v, err := g()
	_ = err
	if v != nil && v.Field != "" {
		fmt.Println(v.Field)
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got != "" {
		t.Fatalf("&& short-circuit guard flagged as nil-deref risk (want no report):\n%s", got)
	}
}

// Guard AFTER the deref: `v.Field != "" && v != nil` evaluates v.Field
// unguarded (short-circuit only protects the RIGHT side). Must still report.
func TestNilDerefLandDerefBeforeGuardStillReports(t *testing.T) {
	src := `package p

func f() {
	v, err := g()
	_ = err
	if v.Field != "" && v != nil {
		_ = v
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got == "" {
		t.Fatal("deref in left conjunct before the guard must still be reported")
	}
}

// err == nil as a conjunct guards every value returned alongside it, for
// later conjuncts AND the if body.
func TestNilDerefLandErrNilConjunctSuppresses(t *testing.T) {
	src := `package p

func f() {
	v, err := g()
	_ = err
	if err == nil && v.Field != "" {
		_ = v.Field
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got != "" {
		t.Fatalf("err == nil conjunct must suppress later conjuncts and the body:\n%s", got)
	}
}

// `err != nil && v.Field != ""` keeps the risk: values are likely nil when
// the error is non-nil. Must still report.
func TestNilDerefLandErrNotNilKeepsRisk(t *testing.T) {
	src := `package p

func f() {
	v, err := g()
	_ = err
	if err != nil && v.Field != "" {
		_ = v
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got == "" {
		t.Fatal("err != nil conjunct must NOT suppress the risk")
	}
}

// || does not short-circuit into a guard: `v.Field != "" || v != nil`
// evaluates v.Field with v at risk. Reporting stays (unchanged #1068 path).
func TestNilDerefLorNotAGuard(t *testing.T) {
	src := `package p

func f() {
	v, err := g()
	_ = err
	if v.Field != "" || v != nil {
		_ = v
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got == "" {
		t.Fatal("|| condition must not suppress the left-side deref")
	}
}

// The else branch of an && condition proves nothing: NOT(a && b) permits any
// conjunct to be false, so a deref in else stays risky.
func TestNilDerefLandElseBranchStaysRisky(t *testing.T) {
	src := `package p

func f() {
	v, err := g()
	_ = err
	if v != nil && v.Field != "" {
		_ = v
	} else {
		_ = v.Field
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got == "" {
		t.Fatal("else branch of && guard must keep the nil risk")
	}
}

// Parenthesized && grouping must not hide the guard: (a && b) && c flattens.
func TestNilDerefLandParenGroupFlattened(t *testing.T) {
	src := `package p

func f() {
	v, err := g()
	_ = err
	if (v != nil && err == nil) && v.Field != "" {
		_ = v.Field
	}
}
`
	if got := checkNilDerefAfterError("x.go", "", src); got != "" {
		t.Fatalf("parenthesized && guard chain must suppress:\n%s", got)
	}
}
