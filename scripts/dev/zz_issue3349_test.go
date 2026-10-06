// Issue #3349 regression tests: the goroutine gate's defer branches must not
// count a recover-defer that sits inside a NESTED closure - Go defers register
// on their lexically enclosing function's defer stack, so such a defer cannot
// recover a panic in the goroutine body itself. Also pins the DeferStmt
// tightening to safego.Recover only (#1426-C alignment).
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// gateFor parses a snippet containing exactly one `go` statement and reports
// the gate verdict for it.
func gateFor(t *testing.T, snippet string) bool {
	t.Helper()
	src := "package p\n\nfunc f() {\n\t" + snippet + "\n}\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatalf("parse snippet %q: %v", snippet, err)
	}
	var gostmt *ast.GoStmt
	ast.Inspect(file, func(n ast.Node) bool {
		if gs, ok := n.(*ast.GoStmt); ok {
			gostmt = gs
			return false
		}
		return true
	})
	if gostmt == nil {
		t.Fatalf("snippet %q contains no go statement", snippet)
	}
	return compliant(fset, gostmt)
}

func TestGateNestedClosureDeferDoesNotProtectGoroutine(t *testing.T) {
	cases := []struct {
		name    string
		snippet string
	}{
		{
			// #3349 PoC case A: safego.Recover deferred inside a helper closure.
			name: "nested safego.Recover",
			snippet: `go func() {
				helper := func() {
					defer safego.Recover("inner")
					innerWork()
				}
				outerRisky()
				helper()
			}()`,
		},
		{
			// #3349 PoC case B: hand-rolled recover inside a helper closure.
			name: "nested manual recover",
			snippet: `go func() {
				helper := func() {
					defer func() { recover() }()
					innerWork()
				}
				outerRisky()
				helper()
			}()`,
		},
		{
			// Nested safego.Recover immediately-invoked: still a closure frame
			// of its own - the outer body is unprotected.
			name: "immediately-invoked nested closure",
			snippet: `go func() {
				outerRisky()
				func() {
					defer safego.Recover("inner")
				}()
			}()`,
		},
	}
	for _, tc := range cases {
		if got := gateFor(t, tc.snippet); got {
			t.Errorf("%s: gate says COMPLIANT but the goroutine frame has no recover (nested-closure bypass, #3349)", tc.name)
		}
	}
}

func TestGateFrameLevelDeferStillCompliant(t *testing.T) {
	cases := []struct {
		name    string
		snippet string
	}{
		{
			name: "direct defer safego.Recover",
			snippet: `go func() {
				defer safego.Recover("outer")
				work()
			}()`,
		},
		{
			name: "direct defer closure with recover",
			snippet: `go func() {
				defer func() { recover() }()
				work()
			}()`,
		},
		{
			name: "defer inside if block in same frame",
			snippet: `go func() {
				if cond() {
					defer safego.Recover("outer")
				}
				work()
			}()`,
		},
		{
			name: "defer inside for block in same frame",
			snippet: `go func() {
				for {
					defer safego.Recover("outer")
					break
				}
			}()`,
		},
	}
	for _, tc := range cases {
		if got := gateFor(t, tc.snippet); !got {
			t.Errorf("%s: gate says NON-COMPLIANT but the defer protects the goroutine frame (false positive)", tc.name)
		}
	}
}

func TestGateCanonicalForms(t *testing.T) {
	if !gateFor(t, `go safego.Go("name", fn)`) {
		t.Error("go safego.Go(...) must stay compliant")
	}
	if !gateFor(t, `go func() { safego.Run("name", fn) }()`) {
		t.Error("single-statement safego.Run body must stay compliant")
	}
}

func TestGateTightenedDeferBranches(t *testing.T) {
	// defer safego.SetLogger has zero recovery semantics - must NOT pass.
	if got := gateFor(t, `go func() {
		defer safego.SetLogger(l)
		work()
	}()`); got {
		t.Error("defer safego.SetLogger passed the gate: DeferStmt branch accepts non-Recover safego.X (#1426-C class, #3349)")
	}
	// #1860 regression pin: prepended statement bypasses the runner-only rule.
	if got := gateFor(t, `go func() {
		doRiskyWork()
		safego.Go("x", fn)
	}()`); got {
		t.Error("prepended statement + safego.Go passed the gate (#1860 regression)")
	}
}
