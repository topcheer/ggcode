package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Issue #2678: detectSendAfterClose must not pair close/send that sit on
// mutually exclusive control-flow paths. #2648 fixed flat if/else siblings
// and early returns; these tests pin the remaining else-if chain case plus
// regression guards for true same-path violations.

// runChannelSafetyOnCode parses src and returns all channel safety instances.
func runChannelSafetyOnCode(t *testing.T, src string) []channelSafetyInstance {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got []channelSafetyInstance
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		got = append(got, analyzeChannelOpsInFunc(fset, fn.Body)...)
	}
	return got
}

func hasSendAfterClose(instances []channelSafetyInstance) bool {
	for _, inst := range instances {
		if inst.kind == "send-after-close" {
			return true
		}
	}
	return false
}

// Scenario 1 (#2678 original): close in `if`, send in `else` -- flat siblings.
// Already fixed by #2648; guard against regression.
func TestIssue2678_IfElseExclusiveNoWarning(t *testing.T) {
	src := `package p
func f(ch chan int, done bool) {
	if done {
		close(ch)
	} else {
		ch <- 1
	}
}
`
	if hasSendAfterClose(runChannelSafetyOnCode(t, src)) {
		t.Fatal("if/else sibling branches are mutually exclusive; must not warn send-after-close")
	}
}

// Scenario 2 (#2678 original): close followed by return -- path terminates.
func TestIssue2678_CloseThenReturnNoWarning(t *testing.T) {
	src := `package p
func f(ch chan int, err error) {
	if err != nil {
		close(ch)
		return
	}
	ch <- 1
}
`
	if hasSendAfterClose(runChannelSafetyOnCode(t, src)) {
		t.Fatal("close followed by return terminates the path; later send is exclusive; must not warn")
	}
}

// Scenario 3 (residual gap after #2648): else-if chain -- the close in the
// first `if` branch and the send in the `else if` branch share the OUTER if
// as exclusive ancestor. #2648's innermost-if-only check missed this.
func TestIssue2678_ElseIfChainExclusiveNoWarning(t *testing.T) {
	src := `package p
func f(ch chan int, a, b bool) {
	if a {
		close(ch)
	} else if b {
		ch <- 1
	}
}
`
	if hasSendAfterClose(runChannelSafetyOnCode(t, src)) {
		t.Fatal("else-if branches are mutually exclusive; must not warn")
	}
}

// Scenario 4: true same-path violation must still warn (regression guard).
func TestIssue2678_TruePositiveStillWarns(t *testing.T) {
	src := `package p
func f(ch chan int, done bool) {
	close(ch)
	ch <- 1
}
`
	insts := runChannelSafetyOnCode(t, src)
	if !hasSendAfterClose(insts) {
		t.Fatal("close then send on the same path must still warn")
	}
	for _, inst := range insts {
		if inst.kind == "send-after-close" && !strings.Contains(inst.posStr, ":") {
			t.Fatalf("instance should carry a source position, got %q", inst.posStr)
		}
	}
}
