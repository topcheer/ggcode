package agent

import (
	"strings"
	"testing"
)

func TestCheckCopylock_NonGoFile(t *testing.T) {
	result := checkCopylock("foo.txt", "", "package main")
	if result != nil {
		t.Fatalf("expected nil for non-Go file, got %v", result)
	}
}

func TestCheckCopylock_EmptyContent(t *testing.T) {
	result := checkCopylock("foo.go", "", "")
	if result != nil {
		t.Fatalf("expected nil for empty content, got %v", result)
	}
}

func TestCheckCopylock_NoIssues(t *testing.T) {
	src := `package main

import "sync"

func doWork(m *sync.Mutex) {
	m.Lock()
	defer m.Unlock()
}
`
	result := checkCopylock("foo.go", "", src)
	if result != nil {
		t.Fatalf("expected no warnings for pointer mutex, got %v", result)
	}
}

func TestCheckCopylock_ValueParam(t *testing.T) {
	src := `package main

import "sync"

func doWork(m sync.Mutex) {
	m.Lock()
	defer m.Unlock()
}
`
	result := checkCopylock("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for value mutex param, got none")
	}
	found := false
	for _, w := range result {
		if strings.Contains(w, "value parameter") && strings.Contains(w, "sync.Mutex") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'value parameter ... sync.Mutex' warning, got %v", result)
	}
}

func TestCheckCopylock_ValueReturn(t *testing.T) {
	src := `package main

import "sync"

func newMutex() sync.Mutex {
	return sync.Mutex{}
}
`
	result := checkCopylock("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for value mutex return, got none")
	}
	found := false
	for _, w := range result {
		if strings.Contains(w, "value return") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'value return ... sync.Mutex' warning, got %v", result)
	}
}

func TestCheckCopylock_ValueReceiver(t *testing.T) {
	src := `package main

import "sync"

type SafeCounter struct {
	mu sync.Mutex
	n  int
}

func (c SafeCounter) Increment() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}
`
	result := checkCopylock("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for value receiver, got none")
	}
}

func TestCheckCopylock_WaitGroupValueParam(t *testing.T) {
	src := `package main

import "sync"

func runner(wg sync.WaitGroup) {
	wg.Done()
}
`
	result := checkCopylock("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for WaitGroup value param, got none")
	}
	found := false
	for _, w := range result {
		if strings.Contains(w, "sync.WaitGroup") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected sync.WaitGroup warning, got %v", result)
	}
}

func TestCheckCopylock_PointerParamNoWarning(t *testing.T) {
	src := `package main

import "sync"

func runner(wg *sync.WaitGroup) {
	wg.Done()
}
`
	result := checkCopylock("foo.go", "", src)
	if result != nil {
		t.Fatalf("expected no warnings for pointer WaitGroup, got %v", result)
	}
}

func TestCheckCopylock_DeltaAware(t *testing.T) {
	oldSrc := `package main

import "sync"

func oldFunc(wg sync.WaitGroup) {
	wg.Done()
}
`
	newSrc := `package main

import "sync"

func oldFunc(wg sync.WaitGroup) {
	wg.Done()
}

func newFunc(m sync.Mutex) {
	m.Lock()
}
`
	result := checkCopylock("foo.go", oldSrc, newSrc)
	if len(result) == 0 {
		t.Fatal("expected at least 1 new warning from delta, got none")
	}

	for _, w := range result {
		if strings.Contains(w, "oldFunc") {
			t.Fatalf("oldFunc (pre-existing) should be filtered: %s", w)
		}
	}
}

func TestCheckCopylock_MaxWarnings(t *testing.T) {
	src := `package main

import "sync"

func f1(a sync.Mutex, b sync.RWMutex) {}
func f2(c sync.WaitGroup, d sync.Once) {}
func f3(e sync.Cond) {}
`
	result := checkCopylock("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings, got none")
	}
	// Should be capped at maxCopylockWarnings + 1 truncation message
	if len(result) > maxCopylockWarnings+1 {
		t.Fatalf("expected at most %d warnings, got %d", maxCopylockWarnings+1, len(result))
	}
}

func TestCheckCopylock_SyncMapValueParam(t *testing.T) {
	src := `package main

import "sync"

func read(m sync.Map) {
	m.Load("key")
}
`
	result := checkCopylock("foo.go", "", src)
	if len(result) == 0 {
		t.Fatal("expected warnings for sync.Map value param, got none")
	}
}

func TestCheckCopylock_NonSyncTypeNoWarning(t *testing.T) {
	src := `package main

type Mutex struct{}

func doWork(m Mutex) {}
`
	result := checkCopylock("foo.go", "", src)
	if result != nil {
		t.Fatalf("expected no warnings for non-sync type, got %v", result)
	}
}

func TestCheckCopylock_InvalidGoNoCrash(t *testing.T) {
	src := `package main

import "sync"

func broken(m sync.Mutex
`
	result := checkCopylock("foo.go", "", src)
	if result != nil {
		t.Fatalf("expected nil for unparseable Go, got %v", result)
	}
}
