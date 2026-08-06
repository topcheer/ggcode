package agent

import (
	"testing"
)

func TestInfiniteLoop_BasicInfinite(t *testing.T) {
	src := `package main

func worker() {
	for {
		doWork()
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !ilContains(warnings[0], "no break/return/panic") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestInfiniteLoop_EmptyBody(t *testing.T) {
	src := `package main

func hang() {
	for {}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !ilContains(warnings[0], "empty body") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestInfiniteLoop_HasBreak(t *testing.T) {
	src := `package main

func worker() {
	for {
		if done {
			break
		}
		doWork()
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasReturn(t *testing.T) {
	src := `package main

func worker() {
	for {
		return
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasPanic(t *testing.T) {
	src := `package main

func worker() {
	for {
		panic("done")
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for panic, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasOsExit(t *testing.T) {
	src := `package main

import "os"

func worker() {
	for {
		os.Exit(1)
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for os.Exit, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasLogFatal(t *testing.T) {
	src := `package main

import "log"

func worker() {
	for {
		log.Fatal("done")
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for log.Fatal, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasRuntimeGoexit(t *testing.T) {
	src := `package main

import "runtime"

func worker() {
	for {
		runtime.Goexit()
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for runtime.Goexit, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasCond(t *testing.T) {
	src := `package main

func worker() {
	for i := 0; i < 10; i++ {
		doWork()
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for bounded loop, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_HasForCond(t *testing.T) {
	src := `package main

func worker() {
	for done {
		doWork()
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for for-cond loop, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_IfElseBothExit(t *testing.T) {
	src := `package main

func worker() {
	for {
		if done {
			break
		} else {
			return
		}
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for if-else exit, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_IfElseOnlyOneExit(t *testing.T) {
	src := `package main

func worker() {
	for {
		if done {
			break
		} else {
			doWork()
		}
	}
}
`
	// One branch has a break, so the loop CAN exit (when done==true).
	// Conservative: don't flag if any exit path exists.
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for partial exit, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_LabeledBreak(t *testing.T) {
	src := `package main

func worker() {
outer:
	for {
		for {
			break outer
		}
	}
}
`
	// Recursive inspect finds "break outer" inside the outer for{} body.
	// Conservative: don't flag if any break exists at any nesting level.
	warnings := checkInfiniteLoop("test.go", "", src)
	// At least the outer loop should not be flagged (break found recursively).
	// Inner for{} also has a break, so it won't be flagged either.
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_NonGoFile(t *testing.T) {
	warnings := checkInfiniteLoop("test.py", "", "for x in range(10): pass")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestInfiniteLoop_EmptyContent(t *testing.T) {
	warnings := checkInfiniteLoop("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestInfiniteLoop_MultipleInfinite(t *testing.T) {
	src := `package main

func a() {
	for {
		x()
	}
}

func b() {
	for {
		y()
	}
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_MaxWarnings(t *testing.T) {
	src := "package main\n\n"
	for i := 0; i < 10; i++ {
		src += "func f" + string(rune('a'+i)) + "() {\n  for {\n    x()\n  }\n}\n"
	}
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != maxInfiniteLoopWarnings+1 { // +1 for truncation message
		t.Fatalf("expected %d warnings (capped + truncation), got %d", maxInfiniteLoopWarnings+1, len(warnings))
	}
}

func TestInfiniteLoop_RangeLoop(t *testing.T) {
	src := `package main

func worker(items []int) {
	for range items {
		doWork()
	}
}
`
	// Range loops are RangeStmt, not ForStmt, so they won't be flagged.
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for range loop, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_GotoExit(t *testing.T) {
	src := `package main

func worker() {
	for {
		if done {
			goto cleanup
		}
		doWork()
	}
cleanup:
}
`
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for goto exit, got %d: %v", len(warnings), warnings)
	}
}

func TestInfiniteLoop_SelectBlock(t *testing.T) {
	src := `package main

func worker() {
	for {
		select {
		case <-ch:
			doWork()
		}
	}
}
`
	// The for{} body has a select statement (not a direct exit).
	// None of the select cases have break/return. Should be flagged.
	warnings := checkInfiniteLoop("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for select without exit, got %d: %v", len(warnings), warnings)
	}
}

func ilContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && ilContainsStr(s, substr))
}

func ilContainsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
