package agent

import (
	"testing"
)

func TestCheckUnboundedRecursion_NoBaseCase(t *testing.T) {
	// Classic: factorial without base case.
	old := ""
	new := `package main

func factorial(n int) int {
	return n * factorial(n-1)
}
`
	warnings := checkUnboundedRecursion("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for unbounded recursion with no base case")
	}
	if !contains(warnings[0], "unbounded recursion") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
	if !contains(warnings[0], "factorial") {
		t.Errorf("warning should mention function name 'factorial': %s", warnings[0])
	}
}

func TestCheckUnboundedRecursion_WithBaseCase(t *testing.T) {
	// Correct: has base case, should NOT warn.
	new := `package main

func factorial(n int) int {
	if n <= 1 {
		return 1
	}
	return n * factorial(n-1)
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for recursion with base case, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_BothBranchesRecurse(t *testing.T) {
	// If/else where both branches recurse unconditionally: no base case.
	new := `package main

func process(n int) int {
	if n > 0 {
		return process(n - 1)
	}
	return process(n + 1)
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected warning: if/else with both branches recursing")
	}
	if !contains(warnings[0], "process") {
		t.Errorf("warning should mention function 'process': %s", warnings[0])
	}
}

func TestCheckUnboundedRecursion_OneBranchExits(t *testing.T) {
	// If/else where else branch returns a constant: has a non-recursive path.
	new := `package main

func process(n int) int {
	if n > 0 {
		return process(n - 1)
	}
	return 0
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning when one branch exits, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_IfWithoutElse(t *testing.T) {
	// if without else: the implicit else path (skipping the if) does not recurse.
	new := `package main

func process(n int) int {
	if n > 100 {
		return process(n - 1)
	}
	return n * 2
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning when if has implicit else, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_DeltaAware(t *testing.T) {
	// Pre-existing unbounded recursion should NOT be flagged (delta-aware).
	old := `package main

func bad(n int) int {
	return bad(n + 1)
}
`
	// Same content: no new issue introduced.
	warnings := checkUnboundedRecursion("main.go", old, old)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for pre-existing recursion, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_NewRecursion(t *testing.T) {
	// Old file had correct function, new file removes base case.
	old := `package main

func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}
`
	new := `package main

func fib(n int) int {
	return fib(n-1) + fib(n-2)
}
`
	warnings := checkUnboundedRecursion("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for newly-introduced unbounded recursion")
	}
	if !contains(warnings[0], "fib") {
		t.Errorf("warning should mention 'fib': %s", warnings[0])
	}
}

func TestCheckUnboundedRecursion_SwitchAllCasesRecurse(t *testing.T) {
	// Switch where every case recurses: no base case.
	new := `package main

func dispatch(state int) int {
	switch state {
	case 0:
		return dispatch(1)
	case 1:
		return dispatch(2)
	default:
		return dispatch(0)
	}
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected warning: switch with all cases recursing")
	}
}

func TestCheckUnboundedRecursion_SwitchOneCaseExits(t *testing.T) {
	// Switch where one case returns without recursing: safe.
	new := `package main

func dispatch(state int) int {
	switch state {
	case 0:
		return dispatch(1)
	default:
		return 42
	}
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning when switch has a non-recursive case, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_NoRecursion(t *testing.T) {
	new := `package main

func add(a, b int) int {
	return a + b
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for non-recursive function, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_NonGoFile(t *testing.T) {
	warnings := checkUnboundedRecursion("script.py", "", "def f(n):\n  return f(n)\n")
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for non-Go file, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_TestFile(t *testing.T) {
	new := `package main

func helper(n int) int {
	return helper(n + 1)
}
`
	warnings := checkUnboundedRecursion("main_test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for test file, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_DeferNotForced(t *testing.T) {
	// defer f() does not execute immediately, so the function body after the
	// defer has a path that does not force recursion.
	new := `package main

func cleanup(n int) {
	defer cleanup(n - 1)
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	// defer is not an unconditional recursion path.
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for defer-only recursion, got: %v", warnings)
	}
}

func TestCheckUnboundedRecursion_ForLoopSkippable(t *testing.T) {
	// A for loop can be skipped if the condition is initially false, so the
	// path that skips the loop does not recurse.
	new := `package main

func search(n int) int {
	for i := 0; i < n; i++ {
		search(i)
	}
	return 0
}
`
	warnings := checkUnboundedRecursion("main.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning: for loop is skippable, got: %v", warnings)
	}
}

// (contains is already defined in reflection_test.go)
