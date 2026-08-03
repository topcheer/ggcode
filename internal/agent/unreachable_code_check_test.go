package agent

import (
	"strings"
	"testing"
)

func TestCheckUnreachableCode_CodeAfterReturn(t *testing.T) {
	newContent := `package main

import "fmt"

func foo() {
	return
	fmt.Println("dead")
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected unreachable code warning for code after return")
	}
	if !strings.Contains(warnings[0], "Unreachable code") {
		t.Errorf("unexpected warning: %v", warnings[0])
	}
	if !strings.Contains(warnings[0], "return") {
		t.Errorf("warning should mention return: %v", warnings[0])
	}
}

func TestCheckUnreachableCode_CodeAfterPanic(t *testing.T) {
	newContent := `package main

func foo() {
	panic("boom")
	cleanup()
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected unreachable code warning for code after panic")
	}
	if !strings.Contains(warnings[0], "panic") {
		t.Errorf("warning should mention panic: %v", warnings[0])
	}
}

func TestCheckUnreachableCode_CodeAfterLogFatal(t *testing.T) {
	newContent := `package main

import "log"

func foo() {
	log.Fatal("die")
	saveState()
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected unreachable code warning for code after log.Fatal")
	}
	if !strings.Contains(warnings[0], "log.Fatal") {
		t.Errorf("warning should mention log.Fatal: %v", warnings[0])
	}
}

func TestCheckUnreachableCode_DeadBranchIfFalse(t *testing.T) {
	newContent := `package main

import "fmt"

func foo() {
	if false {
		fmt.Println("never runs")
	}
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected unreachable branch warning for if false")
	}
	if !strings.Contains(warnings[0], "if false") {
		t.Errorf("warning should mention if false: %v", warnings[0])
	}
}

func TestCheckUnreachableCode_DeadElseBranchIfTrue(t *testing.T) {
	newContent := `package main

import "fmt"

func foo() {
	if true {
		fmt.Println("always runs")
	} else {
		fmt.Println("never runs")
	}
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected unreachable branch warning for else of if true")
	}
	if !strings.Contains(warnings[0], "else branch of 'if true'") {
		t.Errorf("warning should mention else branch of if true: %v", warnings[0])
	}
}

func TestCheckUnreachableCode_NoFalsePositiveNormalReturn(t *testing.T) {
	newContent := `package main

import "fmt"

func foo(x int) int {
	if x > 0 {
		return x
	}
	return -x
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for normal code, got: %v", warnings)
	}
}

func TestCheckUnreachableCode_DeltaAware(t *testing.T) {
	oldContent := `package main

import "fmt"

func foo() {
	return
	fmt.Println("already dead")
}
`
	newContent := oldContent // no change
	warnings := checkUnreachableCode("test.go", oldContent, newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for pre-existing unreachable code (delta-aware), got: %v", warnings)
	}
}

func TestCheckUnreachableCode_NonGoFileSkipped(t *testing.T) {
	warnings := checkUnreachableCode("test.py", "", "def foo():\n  return\n  print('dead')")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckUnreachableCode_CodeAfterBreak(t *testing.T) {
	newContent := `package main

import "fmt"

func foo() {
	for i := 0; i < 10; i++ {
		break
		fmt.Println("dead in loop")
	}
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected unreachable code warning for code after break")
	}
	if !strings.Contains(warnings[0], "break") {
		t.Errorf("warning should mention break: %v", warnings[0])
	}
}

func TestCheckUnreachableCode_SyntaxErrorSkipped(t *testing.T) {
	// Syntax errors should not trigger unreachable code warnings -
	// the syntax check (#3) already handles those.
	newContent := "package main\n\nfunc foo() {\n\treturn\n\tthis is not valid go\n}\n"
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for syntactically invalid file, got: %v", warnings)
	}
}

func TestCheckUnreachableCode_MaxWarningsCap(t *testing.T) {
	newContent := `package main

import "fmt"

func f1() {
	return
	fmt.Println(1)
}

func f2() {
	return
	fmt.Println(2)
}

func f3() {
	return
	fmt.Println(3)
}

func f4() {
	return
	fmt.Println(4)
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) > maxUnreachableWarnings {
		t.Errorf("expected max %d warnings, got %d", maxUnreachableWarnings, len(warnings))
	}
}

func TestCheckUnreachableCode_EmptyDeadBranchNotFlagged(t *testing.T) {
	newContent := `package main

func foo() {
	if false {
	}
}
`
	warnings := checkUnreachableCode("test.go", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty if-false body, got: %v", warnings)
	}
}
