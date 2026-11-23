package agent

import (
	"testing"
)

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestCrossFileImpactScopeDiscrimination(t *testing.T) {
	method := []impactRemovedSymbol{{category: "method", name: "(*Server).run"}}
	fn := []impactRemovedSymbol{{category: "function", name: "helperFn"}}

	cases := []struct {
		name    string
		removed []impactRemovedSymbol
		src     string
		want    bool
	}{
		{"genuine method expression", method, "package x\nfunc f(s *Server) { Server.run() }", true},
		{"genuine package fn call", fn, "package x\nfunc f() { helperFn() }", true},
		{"other type's method", method, "package x\nfunc f(w *Widget) { w.run() }", false},
		{"package-qualified call", method, "package x\nfunc f() { other.run() }", false},
		{"composite literal key", method, "package x\nvar v = Type{run: 1}", false},
		{"method vs package fn", fn, "package x\nfunc f(x *T) { x.helperFn() }", false},
		{"local shadow", fn, "package x\nfunc f() { helperFn := 1; _ = helperFn }", false},
		{"string literal", fn, "package x\nvar s = \"helperFn\"", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := referencesAnyImpactSymbol(c.src, c.removed); got != c.want {
				t.Fatalf("referencesAnyImpactSymbol = %v, want %v", got, c.want)
			}
		})
	}
}
