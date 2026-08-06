package agent

import (
	"testing"
)

func TestDeferArgEval_NoArgs(t *testing.T) {
	src := `package main
func foo() {
	defer mu.Unlock()
	defer cancel()
	defer w.Flush()
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_ClosureSafe(t *testing.T) {
	src := `package main
import "log"
func foo() {
	defer func() {
		log.Println("done")
	}()
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for closure defer, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_FunctionCallArg(t *testing.T) {
	src := `package main
import (
	"fmt"
	"time"
)
func foo(start time.Time) {
	defer fmt.Println(time.Since(start))
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_LogPrintfWithCall(t *testing.T) {
	src := `package main
import (
	"log"
	"time"
)
func foo(start time.Time) {
	defer log.Printf("took %v", time.Since(start))
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_LiteralArgsSafe(t *testing.T) {
	src := `package main
func foo() {
	defer db.Exec("SELECT 1")
	defer fmt.Sprintf("literal")
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for literal args, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_NestedCallArg(t *testing.T) {
	src := `package main
func foo() {
	defer doSomething(computeValue(process(input)))
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for nested call, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_NotGoFile(t *testing.T) {
	warnings := checkDeferArgEval("test.py", "", "defer foo(bar())")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestDeferArgEval_EmptyContent(t *testing.T) {
	warnings := checkDeferArgEval("test.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestDeferArgEval_VarArgSafe(t *testing.T) {
	src := `package main
func foo(name string) {
	defer cleanup(name)
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for variable arg, got %d: %v", len(warnings), warnings)
	}
}

func TestDeferArgEval_MaxWarnings(t *testing.T) {
	src := `package main
func foo() {
	defer a(f1())
	defer b(f2())
	defer c(f3())
	defer d(f4())
	defer e(f5())
	defer f(f6())
	defer g(f7())
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != maxDeferArgWarnings+1 {
		t.Errorf("expected %d warnings (incl truncation), got %d", maxDeferArgWarnings+1, len(warnings))
	}
}

func TestDeferArgEval_MethodCallArg(t *testing.T) {
	src := `package main
func foo() {
	defer log(obj.String())
}`
	warnings := checkDeferArgEval("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for method call arg, got %d: %v", len(warnings), warnings)
	}
}
