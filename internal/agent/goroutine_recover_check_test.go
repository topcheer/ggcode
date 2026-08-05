package agent

import (
	"strings"
	"testing"
)

func TestGoroutineRecover_NoGoStatement(t *testing.T) {
	src := `package main
func work() {
	doSomething()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_GoWithRecover(t *testing.T) {
	src := `package main
func work() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic: %v", r)
			}
		}()
		riskyOperation()
	}()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for goroutine with recover, got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_UnrecoveredGoroutine(t *testing.T) {
	src := `package main
func work() {
	go func() {
		riskyOperation()
	}()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0], "Unrecovered goroutine") {
		t.Errorf("warning should mention 'Unrecovered goroutine', got: %s", warns[0])
	}
	if !strings.Contains(warns[0], "recover") {
		t.Errorf("warning should mention recover, got: %s", warns[0])
	}
}

func TestGoroutineRecover_MultipleUnrecovered(t *testing.T) {
	src := `package main
func work() {
	go func() { a() }()
	go func() { b() }()
	go func() { c() }()
	go func() { d() }()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 3 {
		t.Errorf("expected 3 warnings (capped), got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_DeltaAware(t *testing.T) {
	oldSrc := `package main
func work() {
	go func() { a() }()
}
`
	newSrc := `package main
func work() {
	go func() { a() }()
	go func() { b() }()
}
`
	warns := checkGoroutineRecover("main.go", oldSrc, newSrc)
	if len(warns) != 1 {
		t.Errorf("expected 1 new warning (delta-aware), got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_DeltaAwareNoNew(t *testing.T) {
	src := `package main
func work() {
	go func() { a() }()
}
`
	warns := checkGoroutineRecover("main.go", src, src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings when no new goroutines added, got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_RecoverInNestedDefer(t *testing.T) {
	src := `package main
func work() {
	go func() {
		defer func() {
			recover()
		}()
		doWork()
	}()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for goroutine with nested recover, got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_NamedFuncCall(t *testing.T) {
	src := `package main
func work() {
	go processRequest()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for named func call (not inline literal), got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_SkipTestFiles(t *testing.T) {
	src := `package main
func TestSomething(t *testing.T) {
	go func() { risky() }()
}
`
	warns := checkGoroutineRecover("foo_test.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for test file, got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_SkipNonGo(t *testing.T) {
	src := `console.log("hello")`
	warns := checkGoroutineRecover("main.js", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for .js file, got %d", len(warns))
	}
}

func TestGoroutineRecover_EmptyContent(t *testing.T) {
	warns := checkGoroutineRecover("main.go", "", "")
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for empty content, got %d", len(warns))
	}
}

func TestGoroutineRecover_InvalidSyntax(t *testing.T) {
	src := `package main
func broken( {
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for invalid syntax, got %d", len(warns))
	}
}

func TestGoroutineRecover_RecoverInIfStatement(t *testing.T) {
	src := `package main
func work() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("recovered: %v", r)
			}
		}()
		process()
	}()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 0 {
		t.Errorf("expected 0 warnings for goroutine with recover in if, got %d: %v", len(warns), warns)
	}
}

func TestGoroutineRecover_MixedRecoverAndNot(t *testing.T) {
	src := `package main
func work() {
	go func() {
		defer func() { recover() }()
		safe()
	}()
	go func() {
		unsafe()
	}()
}
`
	warns := checkGoroutineRecover("main.go", "", src)
	if len(warns) != 1 {
		t.Errorf("expected 1 warning (only unsafe goroutine), got %d: %v", len(warns), warns)
	}
}

func TestHasRecoverCall_DirectRecover(t *testing.T) {
	// Verify hasRecoverCall detects bare recover() via AST parsing.
	warns := checkGoroutineRecover("main.go", "", `package main
func work() {
	go func() {
		recover()
		doWork()
	}()
}
`)
	if len(warns) != 0 {
		t.Errorf("goroutine with bare recover() should not be flagged, got %d warnings", len(warns))
	}

	// Verify absence: goroutine without recover is flagged.
	warns2 := checkGoroutineRecover("main.go", "", `package main
func work() {
	go func() {
		doWork()
	}()
}
`)
	if len(warns2) != 1 {
		t.Errorf("goroutine without recover should be flagged, got %d warnings", len(warns2))
	}
}
