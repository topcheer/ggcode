package agent

import (
	"strings"
	"testing"
)

func TestCheckLoopVarCapture_RangeGoroutineCapture(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for loop variable captured in goroutine")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "captured in goroutine") && strings.Contains(w, "item") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected goroutine capture warning for 'item', got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_RangeGoroutineParamSafe(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func(item int) {
			println(item)
		}(item)
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when loop var passed as param, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_RangeRebindSafe(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		item := item
		go func() {
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when loop var rebound, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_ForLoopCapture(t *testing.T) {
	src := `package main

func process(n int) {
	for i := 0; i < n; i++ {
		go func() {
			println(i)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for loop variable captured in for-loop goroutine")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "i") && strings.Contains(w, "for") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected for-loop capture warning for 'i', got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_DeferCapture(t *testing.T) {
	src := `package main

func process(items []string) {
	for _, item := range items {
		defer func() {
			println(item)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for loop variable captured in defer")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "deferred closure") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected defer capture warning, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_NoLoopNoWarn(t *testing.T) {
	src := `package main

func process() {
	go func() {
		println("hello")
	}()
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings without loop, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_GoroutineNotCapturingVar(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println("fixed value")
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when goroutine doesn't use loop var, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_DeltaAware(t *testing.T) {
	oldSrc := `package main

func process(items []int) {
	for _, item := range items {
		go func() {
			println(item)
		}()
	}
}
`
	newSrc := oldSrc
	warnings := checkLoopVarCapture("example.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for pre-existing pattern (delta-aware), got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_NonGoFile(t *testing.T) {
	warnings := checkLoopVarCapture("example.py", "", "for x in items: pass")
	if warnings != nil {
		t.Fatalf("expected nil for non-Go file, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_TestFile(t *testing.T) {
	src := `package main

func process(items []int) {
	for _, item := range items {
		go func() { println(item) }()
	}
}
`
	warnings := checkLoopVarCapture("example_test.go", "", src)
	if warnings != nil {
		t.Fatalf("expected nil for test file, got: %v", warnings)
	}
}

func TestCheckLoopVarCapture_KeyVarCapture(t *testing.T) {
	src := `package main

func process(items []string) {
	for i := range items {
		go func() {
			println(i)
		}()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for key variable captured in goroutine")
	}
}

func TestCheckLoopVarCapture_Capped(t *testing.T) {
	src := `package main

func process(a, b, c, d []int) {
	for _, v := range a {
		go func() { println(v) }()
	}
	for _, v := range b {
		go func() { println(v) }()
	}
	for _, v := range c {
		go func() { println(v) }()
	}
	for _, v := range d {
		go func() { println(v) }()
	}
}
`
	warnings := checkLoopVarCapture("example.go", "", src)
	if len(warnings) > 3 {
		t.Fatalf("expected max 3 warnings, got %d", len(warnings))
	}
}
