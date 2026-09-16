package agent

// #2433: the lock-without-unlock detector was flow-insensitive receiver-set
// matching - ANY same-receiver Unlock anywhere exempted ALL Lock calls, so
// the headline case (guard-clause early return while holding the lock, with
// a normal-path Unlock) was invisible. Conversely, legitimate indirect
// releases (defer s.release(), the method-value idiom) misfired. These pins
// hold the sequential-simulation semantics and the indirect-release
// fallback.

import (
	"strings"
	"testing"
)

func TestIssue2433EarlyReturnHeldLockDetected(t *testing.T) {
	// The headline miss: normal path unlocks, the guard clause does not.
	src := `package main

import "sync"

func f(mu *sync.Mutex, err error) {
	mu.Lock()
	if err != nil {
		return // early exit holds the lock - the headline case
	}
	mu.Unlock()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for early-return-held lock, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "held across return") {
		t.Fatalf("warning should name the early-return hold: %q", warnings[0])
	}
}

func TestIssue2433IndirectReleaseHelperSilent(t *testing.T) {
	// FP side 1: defer s.release() is a legitimate indirect release.
	src := `package main

func (s *server) work() {
	s.mu.Lock()
	defer s.release()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("defer s.release() is a legal indirect release, got %d warnings: %v", len(warnings), warnings)
	}
}

func TestIssue2433MethodValueDeferSilent(t *testing.T) {
	// FP side 2: unlock := mu.Unlock; defer unlock() idiom.
	src := `package main

import "sync"

func f(mu *sync.Mutex) {
	mu.Lock()
	unlock := mu.Unlock
	defer unlock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("method-value defer is a legal release, got %d warnings: %v", len(warnings), warnings)
	}
}

func TestIssue2433DeferIdiomSilent(t *testing.T) {
	// Control: the canonical defer unlock stays silent.
	src := `package main

import "sync"

func f(mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	doWork()
}
`
	if w := checkLockWithoutUnlock("test.go", "", src); len(w) != 0 {
		t.Fatalf("defer mu.Unlock() must stay silent, got %v", w)
	}
}

func TestIssue2433PlainMissingUnlockStillDetected(t *testing.T) {
	// Control: the trivial shape (no Unlock anywhere) keeps warning.
	src := `package main

import "sync"

func f(mu *sync.Mutex) {
	mu.Lock()
	doWork()
	return
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for the trivial missing-unlock shape, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue2433BranchEndLeakDetected(t *testing.T) {
	// The TryLock idiom's forgotten-Unlock shape (branch-scoped hold).
	src := `package main

import "sync"

func f(mu *sync.Mutex) {
	if mu.TryLock() {
		doWork()
		// forgot to Unlock inside the branch
	}
	after()
}
`
	warnings := checkLockWithoutUnlock("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for branch-end leak, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "held at branch end") {
		t.Fatalf("warning should name the branch-end hold: %q", warnings[0])
	}
}

func TestIssue2433PairedBranchLockSilent(t *testing.T) {
	// Control: branch-scoped acquire + in-branch release stays silent.
	src := `package main

import "sync"

func f(mu *sync.Mutex) {
	if mu.TryLock() {
		doWork()
		mu.Unlock()
	}
	after()
}
`
	if w := checkLockWithoutUnlock("test.go", "", src); len(w) != 0 {
		t.Fatalf("paired branch lock/unlock must stay silent, got %v", w)
	}
}
