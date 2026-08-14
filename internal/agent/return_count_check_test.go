package agent

import (
	"strings"
	"testing"
)

func TestCheckExcessiveReturns_NoReturns(t *testing.T) {
	warnings := checkExcessiveReturns("test.go", "", `package main
func simple() {}
`)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckExcessiveReturns_BelowThreshold(t *testing.T) {
	warnings := checkExcessiveReturns("test.go", "", `package main
func process(x int) int {
	if x > 0 { return 1 }
	if x < 0 { return -1 }
	return 0
}
`)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for 3 returns, got %d", len(warnings))
	}
}

func TestCheckExcessiveReturns_AtThreshold(t *testing.T) {
	src := `package main
func complex(x int) int {
	if x == 1 { return 1 }
	if x == 2 { return 2 }
	if x == 3 { return 3 }
	if x == 4 { return 4 }
	if x == 5 { return 5 }
	return 0
}
`
	warnings := checkExcessiveReturns("test.go", "", src)
	if len(warnings) == 0 {
		t.Error("expected warning for 6 returns, got 0")
	}
	if !strings.Contains(warnings[0], "complex") {
		t.Errorf("warning should mention function name, got: %s", warnings[0])
	}
}

func TestCheckExcessiveReturns_DeltaAware(t *testing.T) {
	old := `package main
func complex(x int) int {
	if x == 1 { return 1 }
	if x == 2 { return 2 }
	if x == 3 { return 3 }
	if x == 4 { return 4 }
	if x == 5 { return 5 }
	return 0
}
`
	warnings := checkExcessiveReturns("test.go", old, old)
	if len(warnings) != 0 {
		t.Errorf("delta-aware: expected 0 warnings, got %d", len(warnings))
	}
}

func TestCheckExcessiveReturns_NewInstance(t *testing.T) {
	old := `package main
func complex(x int) int {
	if x == 1 { return 1 }
	if x == 2 { return 2 }
	if x == 3 { return 3 }
	if x == 4 { return 4 }
	if x == 5 { return 5 }
	return 0
}
`
	newContent := old + `
func another(y int) int {
	if y == 1 { return 1 }
	if y == 2 { return 2 }
	if y == 3 { return 3 }
	if y == 4 { return 4 }
	if y == 5 { return 5 }
	return 0
}
`
	warnings := checkExcessiveReturns("test.go", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected 1 warning for newly added function")
	}
	if !strings.Contains(warnings[0], "another") {
		t.Errorf("should flag the NEW function 'another', got: %s", warnings[0])
	}
}

func TestCheckExcessiveReturns_SkipsTestFunctions(t *testing.T) {
	src := `package main
func TestMyFunc(t int) int {
	if t == 1 { return 1 }
	if t == 2 { return 2 }
	if t == 3 { return 3 }
	if t == 4 { return 4 }
	if t == 5 { return 5 }
	if t == 6 { return 6 }
	return 0
}
`
	warnings := checkExcessiveReturns("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for test function, got %d", len(warnings))
	}
}

func TestCheckExcessiveReturns_DoesNotCountNestedFuncLitReturns(t *testing.T) {
	src := `package main
import "fmt"
func outer(x int) int {
	defer func() {
		if x > 0 { fmt.Println("a"); return }
		if x > 1 { fmt.Println("b"); return }
		if x > 2 { fmt.Println("c"); return }
		if x > 3 { fmt.Println("d"); return }
		if x > 4 { fmt.Println("e"); return }
		fmt.Println("f")
	}()
	if x == 1 { return 1 }
	return 0
}
`
	warnings := checkExcessiveReturns("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings (2 outer returns, inner returns belong to closure), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckExcessiveReturns_NonGoFile(t *testing.T) {
	warnings := checkExcessiveReturns("test.py", "", "def f():\n  return 1\n  return 2\n  return 3\n  return 4\n  return 5\n  return 6\n")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckExcessiveReturns_MaxWarnings(t *testing.T) {
	src := `package main
func f1(x int) int { if x > 0 { return 1 }; if x > 1 { return 2 }; if x > 2 { return 3 }; if x > 3 { return 4 }; if x > 4 { return 5 }; return 0 }
func f2(x int) int { if x > 0 { return 1 }; if x > 1 { return 2 }; if x > 2 { return 3 }; if x > 3 { return 4 }; if x > 4 { return 5 }; return 0 }
func f3(x int) int { if x > 0 { return 1 }; if x > 1 { return 2 }; if x > 2 { return 3 }; if x > 3 { return 4 }; if x > 4 { return 5 }; return 0 }
func f4(x int) int { if x > 0 { return 1 }; if x > 1 { return 2 }; if x > 2 { return 3 }; if x > 3 { return 4 }; if x > 4 { return 5 }; return 0 }
func f5(x int) int { if x > 0 { return 1 }; if x > 1 { return 2 }; if x > 2 { return 3 }; if x > 3 { return 4 }; if x > 4 { return 5 }; return 0 }
`
	warnings := checkExcessiveReturns("test.go", "", src)
	if len(warnings) > maxReturnCountWarnings+1 {
		t.Errorf("expected at most %d warnings, got %d", maxReturnCountWarnings+1, len(warnings))
	}
}

func TestCheckExcessiveReturns_LineShiftNotReflagged(t *testing.T) {
	old := `package main
func complex(x int) int {
	if x == 1 { return 1 }
	if x == 2 { return 2 }
	if x == 3 { return 3 }
	if x == 4 { return 4 }
	if x == 5 { return 5 }
	return 0
}
`
	// Same function, 5 unrelated lines inserted above it (#157).
	shifted := `package main

// unrelated comment 1
// unrelated comment 2
// unrelated comment 3
// unrelated comment 4
// unrelated comment 5
func complex(x int) int {
	if x == 1 { return 1 }
	if x == 2 { return 2 }
	if x == 3 { return 3 }
	if x == 4 { return 4 }
	if x == 5 { return 5 }
	return 0
}
`
	warnings := checkExcessiveReturns("test.go", old, shifted)
	if len(warnings) != 0 {
		t.Errorf("#157 regression: line shift re-flagged pre-existing issue: %v", warnings)
	}
}
