package agent

import "testing"

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
