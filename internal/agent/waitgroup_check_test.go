package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestCheckWaitGroup_DoneWithoutDefer(t *testing.T) {
	new := `package main
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	if len(warnings) == 0 {
		t.Fatal("expected Done-without-defer warning, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "without defer") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'without defer' warning, got: %v", warnings)
	}
}

func TestCheckWaitGroup_NoWarningForDeferDone(t *testing.T) {
	new := `package main
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	for _, w := range warnings {
		if strings.Contains(w, "without defer") {
			t.Errorf("should not warn for defer wg.Done(), got: %s", w)
		}
	}
}

func TestCheckWaitGroup_DoneWithoutAdd(t *testing.T) {
	// #938: a function RECEIVING *sync.WaitGroup that only calls Done/Wait
	// is the canonical worker/waiter split - Add() lives in the spawner,
	// invisible to function-granularity analysis. Must NOT warn.
	new := `package main
import "sync"
func work(wg *sync.WaitGroup) {
	go func() {
		defer wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	for _, w := range warnings {
		if strings.Contains(w, "Add() is never called") {
			t.Errorf("worker taking wg as param must not warn (#938 canonical shape), got: %s", w)
		}
	}
}

// #938: Done-without-Add still warns when the function OWNS the WaitGroup
// (declared locally, not received as a parameter) - there is no spawner
// elsewhere that could call Add().
func TestCheckWaitGroup_DoneWithoutAddLocalWG(t *testing.T) {
	new := `package main
import "sync"
func work() {
	var wg sync.WaitGroup
	go func() {
		defer wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Add() is never called") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("locally-declared wg with Done but no Add must still warn, got: %v", warnings)
	}
}

func TestCheckWaitGroup_AddInsideGoroutine(t *testing.T) {
	new := `package main
import "sync"
func work(wg *sync.WaitGroup) {
	go func() {
		wg.Add(1)
		defer wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "inside a goroutine") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Add-inside-goroutine warning, got: %v", warnings)
	}
}

func TestCheckWaitGroup_NoWarningForCorrectUsage(t *testing.T) {
	new := `package main
import "sync"
func work(items []string) {
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			_ = s
		}(item)
	}
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for correct WaitGroup usage, got: %v", warnings)
	}
}

func TestCheckWaitGroup_SkipsNonGoFiles(t *testing.T) {
	warnings := checkWaitGroupMisuse("work.py", "", "import sync\nwg = sync.WaitGroup()")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go files")
	}
}

func TestCheckWaitGroup_SkipsTestFiles(t *testing.T) {
	new := `package main
import "sync"
func TestWork(t *testing.T) {
	var wg sync.WaitGroup
	wg.Done()
}`
	warnings := checkWaitGroupMisuse("work_test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test files")
	}
}

func TestCheckWaitGroup_NoWarningWhenPreExisting(t *testing.T) {
	code := `package main
import "sync"
func work(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", code, code)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for pre-existing issues (delta), got: %v", warnings)
	}
}

func TestCheckWaitGroup_EmptyContent(t *testing.T) {
	warnings := checkWaitGroupMisuse("work.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content")
	}
}

func TestCheckWaitGroup_NoWaitGroupInSource(t *testing.T) {
	new := `package main
func work() {
	x.Done()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when source has no WaitGroup reference, got: %v", warnings)
	}
}

func TestCheckWaitGroup_InvalidGoSyntax(t *testing.T) {
	warnings := checkWaitGroupMisuse("work.go", "", "this is not valid go code with WaitGroup")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for invalid Go syntax")
	}
}

func TestCheckWaitGroup_DetectsMultiplePatterns(t *testing.T) {
	// Both: Done without defer AND Add inside goroutine
	new := `package main
import "sync"
func work(wg *sync.WaitGroup) {
	go func() {
		wg.Add(1)
		wg.Done()
	}()
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckWaitGroup_AddBeforeGoNotFlagged(t *testing.T) {
	// Add is before the go statement - correct pattern, not flagged for race
	new := `package main
import "sync"
func work(items []string) {
	var wg sync.WaitGroup
	for range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
		}()
	}
	wg.Wait()
}`
	warnings := checkWaitGroupMisuse("work.go", "", new)
	for _, w := range warnings {
		if strings.Contains(w, "inside a goroutine") {
			t.Errorf("should not flag Add-before-go as race condition, got: %s", w)
		}
	}
}

func TestFindWaitGroupMisuse_EmptyString(t *testing.T) {
	if issues := findWaitGroupMisuse(""); issues != nil {
		t.Errorf("expected nil for empty string, got %v", issues)
	}
}

func TestCollectWGStats_NoWGMethods(t *testing.T) {
	// Should return zero stats for code with no WaitGroup-like method calls
	src := `package main
func work() {
	x := 1
	_ = x
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			stats := collectWGStats(fn.Body)
			if stats.addTotal != 0 || stats.doneBare != 0 || stats.doneDefer != 0 || stats.waitTotal != 0 {
				t.Errorf("expected zero stats, got %+v", stats)
			}
		}
	}
}
