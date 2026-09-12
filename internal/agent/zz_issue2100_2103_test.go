package agent

// #2100/#2103 regression:
//   - #2103: exploration fragmentation cleared the window BEFORE reading
//     len(s.entries) for the debug log and the message's call count -
//     every firing said "0 exploration tool calls across 6+ distinct
//     targets" (mathematically self-contradictory).
//   - #2100: go/parser never sets Obj on SelectorExpr.Sel or composite
//     literal keys, so cross-file-impact fired on any same-named method
//     of ANY type, package-qualified calls, and field keys.

import (
	"strings"
	"testing"
)

func TestExplorationFragMessageCountsNotZero(t *testing.T) {
	s := newExploreFragState()
	// recordToolCall returns the analysis message when it fires.
	var msg string
	for i := 0; i < 6; i++ {
		msg = s.recordToolCall("read_file", []byte(`{"path":"/repo/file`+string(rune('a'+i))+`"}`), 2+i%2)
	}
	if msg == "" {
		t.Fatal("expected the detector to fire (6 calls / 6 unique targets)")
	}
	if strings.Contains(msg, "] 0 exploration tool calls") {
		t.Fatalf("message reports 0 calls after fire-and-clear (order regression): %q", msg[:min2(90, len(msg))])
	}
	if !strings.Contains(msg, "] 6 exploration tool calls") {
		t.Fatalf("message must report the pre-clear call count: %q", msg[:min2(90, len(msg))])
	}
}

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
