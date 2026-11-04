package codehealth

import (
	"testing"
)

// Issue #5037: the IfStmt branch of nestingDepthOfStmt returned early from
// the then-branch (its guard was unconditionally true), so the else branch
// was never traversed -- deep nesting inside else (or else-if chains) was
// systematically undercounted.

func nestingOf5037(t *testing.T, src string) FuncMetrics {
	t.Helper()
	funcs, err := analyzeSource("probe.go", []byte("package p\n"+src))
	if err != nil {
		t.Fatal(err)
	}
	if len(funcs) == 0 {
		t.Fatal("no functions analyzed")
	}
	return funcs[0]
}

func TestIssue5037ElseBranchNestingCounted(t *testing.T) {
	// Issue repro: three nested for-loops inside else => real depth 4
	// (func body counts as 0, if=1, for=2,3,4).
	m := nestingOf5037(t, "func f(b bool) {\n\tif b {\n\t} else {\n\t\tfor { for { for {} } }\n\t}\n}\n")
	if m.NestingDepth != 4 {
		t.Fatalf("else-branch nesting must count: want 4, got %d", m.NestingDepth)
	}
}

func TestIssue5037ElseIfChainNestingCounted(t *testing.T) {
	// else-if chain: the chain desugars to a nested if inside else, so each
	// else-if level adds one: if(a)=1, if(b)=2, for=3,4 => 4.
	m := nestingOf5037(t, "func f(a, b bool) {\n\tif a {\n\t} else if b {\n\t\tfor { for {} }\n\t} else {\n\t}\n}\n")
	if m.NestingDepth != 4 {
		t.Fatalf("else-if chain nesting must count: want 4, got %d", m.NestingDepth)
	}
}

func TestIssue5037ThenBranchUnchanged(t *testing.T) {
	// The then-branch already counted correctly; the fix must not regress it.
	m := nestingOf5037(t, "func f(b bool) {\n\tif b {\n\t\tfor { for { for {} } }\n\t}\n}\n")
	if m.NestingDepth != 4 {
		t.Fatalf("then-branch nesting regression: want 4, got %d", m.NestingDepth)
	}
}

func TestIssue5037ShallowStaysShallow(t *testing.T) {
	m := nestingOf5037(t, "func f(b bool) {\n\tif b {\n\t\t_ = 1\n\t} else {\n\t\t_ = 2\n\t}\n}\n")
	if m.NestingDepth != 1 {
		t.Fatalf("flat if/else must stay depth 1, got %d", m.NestingDepth)
	}
}
