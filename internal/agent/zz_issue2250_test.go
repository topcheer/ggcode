package agent

// #2250: the #2242 nested-loop barrier covered only loopBodyHasErrorRetry;
// branchContinuesOnError and loopBodyHasFailingCall - the other two halves
// of the isRetryLoop conjunction - still descended into nested loops, so
// the exact #1490-C misattribution returned via the if-body shape: an
// inner range's skip-continue plus an inner failing call sufficed to flag
// the OUTER loop. Both scanners now stop at nested for/range.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func parse2250(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "t.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestIssue2250NestedLoopBarrierBothScanners(t *testing.T) {
	// Issue shape: outer for + if err!=nil{...;continue} + inner range
	// carrying the ONLY failing call and its OWN skip-continue.
	f := parse2250(t, `package p
import "net/http"
func f(urls []string) {
	for _, g := range groups {
		if g.Err != nil {
			log(g.Err) // outer has NO continue of its own
		}
		for _, u := range g.URLs {
			resp, err := http.Get(u)
			if err != nil {
				continue
			}
			resp.Body.Close()
		}
	}
}
`)
	var outer ast.Stmt
	ast.Inspect(f, func(n ast.Node) bool {
		if outer != nil {
			return false
		}
		switch st := n.(type) {
		case *ast.ForStmt:
			outer = st
			return false
		case *ast.RangeStmt:
			outer = st
			return false
		}
		return true
	})
	if outer == nil {
		t.Fatal("no outer loop")
	}
	var body *ast.BlockStmt
	switch st := outer.(type) {
	case *ast.ForStmt:
		body = st.Body
	case *ast.RangeStmt:
		body = st.Body
	}
	if loopBodyHasFailingCall(body) {
		t.Fatal("inner-range failing call must not attribute to the outer loop")
	}
	if branchContinuesOnError(body) {
		t.Fatal("inner-range skip-continue must not attribute to the outer loop")
	}
}

func TestIssue2250DirectCallAndContinueStillFlag(t *testing.T) {
	// Positive control: no nesting - the failing call and the continue
	// sit DIRECTLY in the outer body. Both halves must still fire.
	f := parse2250(t, `package p
import "net/http"
func f(urls []string) {
	for _, u := range urls {
		resp, err := http.Get(u)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}
`)
	var loop *ast.RangeStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if rs, ok := n.(*ast.RangeStmt); ok {
			loop = rs
			return false
		}
		return loop == nil
	})
	if loop == nil {
		t.Fatal("no range loop")
	}
	if !loopBodyHasFailingCall(loop.Body) {
		t.Fatal("direct failing call must still count")
	}
	if !branchContinuesOnError(loop.Body) {
		t.Fatal("direct skip-continue must still count")
	}
}
