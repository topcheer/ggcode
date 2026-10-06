package agent

import (
	"strings"
	"testing"
)

// Issue #2740: balanced double-lock (mu.Lock(); mu.Lock(); mu.Unlock();
// mu.Unlock()) must warn - Go mutexes are not reentrant, the second Lock
// deadlocks at runtime, and go vet/staticcheck do not cover it (the exact
// gap the checker's header failure mode #3 promises to catch).
func TestIssue2740BalancedDoubleLockWarns(t *testing.T) {
	src := `package x
import "sync"
func f(mu *sync.Mutex) {
	mu.Lock()
	doSomething()
	mu.Lock()
	doMore()
	mu.Unlock()
	mu.Unlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning for balanced double-lock, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "double lock") {
		t.Errorf("warning should mention double lock, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "mu.Lock") {
		t.Errorf("warning should name mu.Lock, got: %s", warnings[0])
	}
}

// RLock re-entry is legal (sync.RWMutex reader reentrancy): no warning.
func TestIssue2740RLockReentrySilent(t *testing.T) {
	src := `package x
import "sync"
func f(rw *sync.RWMutex) {
	rw.RLock()
	rw.RLock()
	rw.RUnlock()
	rw.RUnlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("RLock re-entry is legal, expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

// Lock while already holding the same receiver via RLock is also a real
// deadlock (writer blocks on the reader held by self): must warn.
func TestIssue2740LockOverRLockWarns(t *testing.T) {
	src := `package x
import "sync"
func f(rw *sync.RWMutex) {
	rw.RLock()
	rw.Lock()
	rw.Unlock()
	rw.RUnlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning for Lock over held RLock, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "double lock") {
		t.Errorf("warning should mention double lock, got: %s", warnings[0])
	}
}

// Lock in a sequential branch after an outer Lock on the same receiver
// (nested branch copy) still warns: the branch inherits the outer held set.
func TestIssue2740DoubleLockInBranchWarns(t *testing.T) {
	src := `package x
import "sync"
func f(mu *sync.Mutex, cond bool) {
	mu.Lock()
	if cond {
		mu.Lock()
		mu.Unlock()
	}
	mu.Unlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning for branch double-lock, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "double lock") {
		t.Errorf("warning should mention double lock, got: %s", warnings[0])
	}
}

// Delta path: pre-existing double-lock in old content is not re-flagged.
func TestIssue2740DeltaSuppressesPreexisting(t *testing.T) {
	src := `package x
import "sync"
func f(mu *sync.Mutex) {
	mu.Lock()
	mu.Lock()
	mu.Unlock()
	mu.Unlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", src, src)
	if len(warnings) != 0 {
		t.Fatalf("delta check should suppress pre-existing double-lock, got %d: %v", len(warnings), warnings)
	}
}

// Sanity: ordinary balanced Lock/Unlock still silent (no regression).
func TestIssue2740BalancedSingleLockSilent(t *testing.T) {
	src := `package x
import "sync"
func f(mu *sync.Mutex) {
	mu.Lock()
	doSomething()
	mu.Unlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("balanced single Lock/Unlock should be silent, got %d: %v", len(warnings), warnings)
	}
}
