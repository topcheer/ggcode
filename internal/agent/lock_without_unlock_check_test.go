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

	new := `package main

func broken( {
	`
	warnings := checkLockWithoutUnlock("test.go", "", new)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unparseable file, got %d", len(warnings))
	}
}

func TestCheckLockWithoutUnlock_Issue1099_ContentAnchorDelta(t *testing.T) {

	old := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
}

func helper() {
	helperWork()
}
`

	new := `package main
// New comment line
// Another comment

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
}

func helper() {
	helperWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("Issue #1099: expected 0 warnings (content anchor delta should recognize same lock), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_Issue1099_DifferentFuncNameTriggersDelta(t *testing.T) {
	old := `package main

import "sync"

func worker(mu *sync.Mutex) {
	mu.Lock()
	doWork()
}
`

	new := `package main

import "sync"

func workerV2(mu *sync.Mutex) {
	mu.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("Issue #1099: expected 1 warning (funcName changed), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckLockWithoutUnlock_ClosureBodies(t *testing.T) {
	body := func(fn string) string {
		return "package main\n\nimport \"sync\"\n\nvar mu sync.Mutex\n\n" + fn + "\n"
	}
	cases := []struct {
		name string
		fn   string
		want int
	}{
		{"go closure leak", `func w() { go func() { mu.Lock(); process() }() }`, 1},
		{"go closure correct", `func w() { go func() { mu.Lock(); defer mu.Unlock(); process() }() }`, 0},
		{"assigned funclit leak", `func w() { f := func() { mu.Lock(); process() }; f() }`, 1},
		{"assigned funclit correct", `func w() { f := func() { mu.Lock(); defer mu.Unlock() }; f() }`, 0},
		{"immediately invoked funclit leak", `func w() { func() { mu.Lock() }() }`, 1},
		{"direct leak still detected", `func w() { mu.Lock(); process() }`, 1},
		{"outer return not polluted by closure frame", `func w() { f := func() { mu.Lock() }; _ = f; return }`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := checkLockWithoutUnlock("test.go", "", body(tc.fn))
			if got := len(warnings); got != tc.want {
				t.Errorf("%s: warnings=%d want %d (got %v)", tc.name, got, tc.want, warnings)
			}
		})
	}
}

func TestCheckLockWithoutUnlock_SwitchSelectCaseBodies(t *testing.T) {
	body := func(fn string) string {
		return "package main\n\nimport \"sync\"\n\nvar mu sync.Mutex\n\n" + fn + "\n"
	}
	cases := []struct {
		name string
		fn   string
		want int
	}{

		{"switch case body lock leak", `func w(v int) { switch v { case 1: mu.Lock(); process() } }`, 1},
		{"switch default body lock leak", `func w(v int) { switch v { default: mu.Lock() } }`, 1},
		{"type switch case body lock leak", `func w(v any) { switch v.(type) { case int: mu.Lock() } }`, 1},
		{"select case body lock leak", `func w(a, b chan int) { select { case <-a: mu.Lock(); process() } }`, 1},

		{"select per-case unlock all covered", `func w(a, b chan int) { mu.Lock(); select { case <-a: mu.Unlock(); case <-b: mu.Unlock() } }`, 0},
		{"switch per-case unlock all covered", `func w(v int) { mu.Lock(); switch v { case 1: mu.Unlock(); default: mu.Unlock() } }`, 0},
		{"case body unlock only on taken path", `func w(v int) { mu.Lock(); switch { case v > 0: mu.Unlock() } }`, 1},
		{"select one case leaks", `func w(a, b chan int) { mu.Lock(); select { case <-a: mu.Unlock(); case <-b: process() } }`, 1},

		{"case body balanced lock pair", `func w(v int) { switch v { case 1: mu.Lock(); process(); mu.Unlock() } }`, 0},

		{"comm clause assignment body", `func w(ch chan int) { select { case v := <-ch: mu.Lock(); use(v); mu.Unlock() } }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := checkLockWithoutUnlock("test.go", "", body(tc.fn))
			if got := len(warnings); got != tc.want {
				t.Errorf("%s: warnings=%d want %d (got %v)", tc.name, got, tc.want, warnings)
			}
		})
	}
}
