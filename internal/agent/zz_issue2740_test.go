package agent

import (
	"strings"
	"testing"
)

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
