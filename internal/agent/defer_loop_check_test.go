package agent

import (
	"strings"
	"testing"
)

func TestCheckDeferInLoop_DetectsNewDeferInForLoop(t *testing.T) {
	old := `package main
func process(items []string) {
	for _, item := range items {
		_ = item
	}
}`
	new := `package main
import "os"
func process(items []string) {
	for _, item := range items {
		f, _ := os.Open(item)
		defer f.Close()
		_ = f
	}
}`
	warnings := checkDeferInLoop("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected defer-in-loop warning, got none")
	}
	if !strings.Contains(warnings[0], "defer statement") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestCheckDeferInLoop_DetectsDeferInNestedBlockWithinLoop(t *testing.T) {
	old := `package main
func process(items []string) {
	for _, item := range items {
		_ = item
	}
}`
	new := `package main
import "os"
func process(items []string) {
	for _, item := range items {
		if item != "" {
			f, _ := os.Open(item)
			defer f.Close()
			_ = f
		}
	}
}`
	warnings := checkDeferInLoop("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected defer-in-loop warning for defer in nested if within loop")
	}
}

func TestCheckDeferInLoop_NoWarningForDeferOutsideLoop(t *testing.T) {
	old := `package main
func process(items []string) {
	for _, item := range items {
		_ = item
	}
}`
	new := `package main
import "os"
func process(items []string) {
	f, _ := os.Open("config")
	defer f.Close()
	for _, item := range items {
		_ = item
	}
}`
	warnings := checkDeferInLoop("process.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for defer outside loop, got: %v", warnings)
	}
}

func TestCheckDeferInLoop_NoWarningWhenPreExisting(t *testing.T) {
	// Both old and new have the same defer-in-loop - no delta.
	code := `package main
import "os"
func process(items []string) {
	for _, item := range items {
		f, _ := os.Open(item)
		defer f.Close()
		_ = f
	}
}`
	warnings := checkDeferInLoop("process.go", code, code)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when defer-in-loop is pre-existing, got: %v", warnings)
	}
}

func TestCheckDeferInLoop_SkipsNonGoFiles(t *testing.T) {
	warnings := checkDeferInLoop("process.py", "", "for item in items:\n    pass")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go files")
	}
}

func TestCheckDeferInLoop_SkipsTestFiles(t *testing.T) {
	new := `package main
import "os"
func TestProcess(t *testing.T) {
	for _, item := range []string{"a", "b"} {
		f, _ := os.Open(item)
		defer f.Close()
		_ = f
	}
}`
	warnings := checkDeferInLoop("process_test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test files")
	}
}

func TestCheckDeferInLoop_DetectsMultipleDefersInLoop(t *testing.T) {
	old := `package main
func process(items []string) {
	for _, item := range items {
		_ = item
	}
}`
	new := `package main
import (
	"os"
	"sync"
)
func process(items []string) {
	var mu sync.Mutex
	for _, item := range items {
		f, _ := os.Open(item)
		defer f.Close()
		mu.Lock()
		defer mu.Unlock()
		_ = item
	}
}`
	warnings := checkDeferInLoop("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected defer-in-loop warnings")
	}
	if !strings.Contains(warnings[0], "2 defer statements") {
		t.Errorf("expected 2 defer statements, got: %s", warnings[0])
	}
}

func TestCheckDeferInLoop_EmptyContent(t *testing.T) {
	warnings := checkDeferInLoop("process.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content")
	}
}

func TestFindDeferInLoops_SyntacticallyInvalidGo(t *testing.T) {
	results := findDeferInLoops("bad.go", "this is not valid go code")
	if results != nil {
		t.Errorf("expected nil for invalid Go code, got %v", results)
	}
}
