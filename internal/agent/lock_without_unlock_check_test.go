package agent

import (
	"testing"
)

func TestCheckLockWithoutUnlock_DetectsMissingDeferUnlock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_NoWarningWhenDeferUnlockPresent(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_NoWarningWhenDirectUnlockPresent(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
	mu.Unlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_RWMutexRLock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func reader(rw *sync.RWMutex) {
	rw.RLock()
	readData()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for RLock without RUnlock, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_RWMutexRLockWithDeferRUnlock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func reader(rw *sync.RWMutex) {
	rw.RLock()
	defer rw.RUnlock()
	readData()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_DeltaAware_NoNewInstance(t *testing.T) {
	// Pre-existing lock-without-unlock should NOT trigger when file is edited
	// for unrelated reasons.
	old := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
}
`
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
	doMoreWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings (delta-aware), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_DeltaAware_NewInstance(t *testing.T) {
	old := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	doWork()
}
`
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	doWork()
}

func broken(mu *sync.Mutex) {
	mu.Lock()
	forgetToUnlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 new warning (delta-aware), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_StructFieldReceiver(t *testing.T) {
	old := ""
	new := `package main

import "sync"

type Server struct {
	mu sync.Mutex
}

func (s *Server) handle() {
	s.mu.Lock()
	serve()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for struct field receiver, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_StructFieldWithDeferUnlock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

type Server struct {
	mu sync.Mutex
}

func (s *Server) handle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	serve()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_NonGoFile(t *testing.T) {
	warnings := checkLockWithoutUnlock("test.py", "", "mu.Lock()\n")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckLockWithoutUnlock_EmptyContent(t *testing.T) {
	warnings := checkLockWithoutUnlock("test.go", "", "   \n  ")
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckLockWithoutUnlock_NoLockCalls(t *testing.T) {
	new := `package main

func worker() {
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestCheckLockWithoutUnlock_TryLock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	if mu.TryLock() {
		doWork()
		// forgot to Unlock
	}
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for TryLock without Unlock, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_MultipleLocksAllUnlocked(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(m1, m2 *sync.Mutex) {
	m1.Lock()
	defer m1.Unlock()
	m2.Lock()
	defer m2.Unlock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_MultipleLocksOneMissing(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(m1, m2 *sync.Mutex) {
	m1.Lock()
	defer m1.Unlock()
	m2.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (m2 missing Unlock), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_SyntaxErrorSkips(t *testing.T) {
	// Malformed Go should not crash the check.
	new := `package main

func broken( {
`
	warnings := checkLockWithoutUnlock("test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unparseable file, got %d", len(warnings))
	}
}
