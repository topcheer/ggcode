package agent

import (
	"strings"
	"testing"
)

func TestCheckRaceVerifyHint_NewGoroutine(t *testing.T) {
	old := `package main

func processData() {
	data := []int{1, 2, 3}
	_ = data
}
`
	new := `package main

func processData() {
	go func() {
		data := []int{1, 2, 3}
		_ = data
	}()
}
`
	warnings := checkRaceVerifyHint("handler.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "go test -race") {
		t.Errorf("warning should mention 'go test -race', got: %s", warnings[0])
	}
}

func TestCheckRaceVerifyHint_NewSyncMutex(t *testing.T) {
	old := `package main

type Server struct {
	data map[string]int
}
`
	new := `package main

import "sync"

type Server struct {
	mu   sync.Mutex
	data map[string]int
}
`
	warnings := checkRaceVerifyHint("server.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for new sync.Mutex, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_NewWaitGroup(t *testing.T) {
	old := `package main

func worker() {}
`
	new := `package main

import "sync"

func worker() {
	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}
`
	warnings := checkRaceVerifyHint("worker.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for new sync.WaitGroup, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_NewAtomicOp(t *testing.T) {
	old := `package main

var counter int
`
	new := `package main

import "sync/atomic"

var counter int64

func increment() {
	atomic.AddInt64(&counter, 1)
}
`
	warnings := checkRaceVerifyHint("counter.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for atomic usage, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_NoChange(t *testing.T) {
	// Same concurrency primitives, no new additions -- should NOT fire.
	content := `package main

import "sync"

func worker() {
	var mu sync.Mutex
	mu.Lock()
	defer mu.Unlock()
}
`
	warnings := checkRaceVerifyHint("worker.go", content, content)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unchanged content, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_ConcurrencyRemoved(t *testing.T) {
	// Removing goroutine -- should NOT fire (delta is negative).
	old := `package main

func worker() {
	go process()
}
`
	new := `package main

func worker() {
	process()
}
`
	warnings := checkRaceVerifyHint("worker.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings when goroutine removed, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_NonGoFile(t *testing.T) {
	warnings := checkRaceVerifyHint("handler.py", "", "import threading\n")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckRaceVerifyHint_TestFile(t *testing.T) {
	// Test files should not trigger the hint.
	old := `package main`
	new := `package main

import "sync"
import "testing"

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}
`
	warnings := checkRaceVerifyHint("worker_test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for test file, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_NewSyncMap(t *testing.T) {
	old := `package main

type Cache struct {
	data map[string]string
}
`
	new := `package main

import "sync"

type Cache struct {
	data sync.Map
}
`
	warnings := checkRaceVerifyHint("cache.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for sync.Map, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckRaceVerifyHint_EmptyNew(t *testing.T) {
	warnings := checkRaceVerifyHint("worker.go", "package main", "")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty new content, got %d", len(warnings))
	}
}

func TestCheckRaceVerifyHint_ChannelSend(t *testing.T) {
	old := `package main

func process() {
	x := 1
	_ = x
}
`
	new := `package main

func process() {
	ch := make(chan int)
	ch <- 42
}
`
	warnings := checkRaceVerifyHint("process.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for channel send, got %d: %v", len(warnings), warnings)
	}
}

func TestCountConcurrencyPrimitives_Empty(t *testing.T) {
	if countConcurrencyPrimitives("") != 0 {
		t.Error("expected 0 for empty string")
	}
}

func TestCountConcurrencyPrimitives_InvalidGo(t *testing.T) {
	// Should fall back to string matching for unparseable code.
	score := countConcurrencyPrimitives("this is not valid Go code at all")
	if score < 0 {
		t.Errorf("score should be non-negative, got %d", score)
	}
}
