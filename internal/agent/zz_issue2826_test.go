package agent

import (
	"strings"
	"testing"
)

// #2826: fnHasIndirectRelease treated ANY `defer <Ident>()` as an indirect
// release and exempted the whole function from lock simulation. defer
// cancel() (context), defer close(ch) (builtin), defer cleanup()/done() are
// ubiquitous non-lock defers, so a missing mu.Unlock() beside them was
// systematically invisible. Only unlock/release-named bare defers (the
// `v := mu.Unlock; defer v()` method-value idiom, #2433 FP side) keep the
// whole-function exemption.

func TestIssue2826_DeferCancelDoesNotExemptMissingUnlock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	mu.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("defer cancel() must not exempt a missing Unlock (#2826): expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "mu") {
		t.Fatalf("warning should reference the un-released receiver mu: %v", warnings[0])
	}
}

func TestIssue2826_DeferCloseDoesNotExemptMissingUnlock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex, ch chan struct{}) {
	defer close(ch)
	mu.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("defer close(ch) must not exempt a missing Unlock (#2826): expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue2826_DeferCleanupDoesNotExemptMissingUnlock(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	defer cleanup()
	mu.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("defer cleanup() must not exempt a missing Unlock (#2826): expected 1 warning, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue2826_MethodValueUnlockIdiomStillExempt(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	unlock := mu.Unlock
	defer unlock()
	mu.Lock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("defer unlock() method-value idiom keeps the #2433 whole-function exemption: expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue2826_DeferCancelWithProperUnlockStillSilent(t *testing.T) {
	old := ""
	new := `package main

import "sync"

func worker(mu *sync.Mutex) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
	mu.Lock()
	defer mu.Unlock()
	doWork()
}
`
	warnings := checkLockWithoutUnlock("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("proper defer mu.Unlock() must stay silent even beside defer cancel(): expected 0 warnings, got %d: %v", len(warnings), warnings)
	}
}
