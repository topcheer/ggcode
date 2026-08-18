package main

import (
	"sync"
	"testing"
	"time"
)

// #700: lastCloseAttempt is touched from four goroutines (OnBeforeClose,
// SetWindowFocused, hotkey poller, tray CGO callbacks). It is now an
// atomic.Pointer[time.Time]; concurrent Load/Store must be race-free.
// Run with -race to make the data race detection meaningful.
func TestIssue700LastCloseAttemptConcurrentAccess(t *testing.T) {
	a := NewApp()

	const goroutines = 8
	const ops = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				if g%2 == 0 {
					now := time.Now()
					a.lastCloseAttempt.Store(&now)
				} else {
					a.lastCloseAttempt.Store(nil)
				}
				// Every goroutine also reads (mirrors the toggle-direction read).
				_ = a.lastCloseAttempt.Load()
			}
		}(g)
	}
	wg.Wait()
}

// #700: the toggle-direction semantics survive the atomic conversion — a
// stored timestamp is visible to a later Load, and Store(nil) clears it.
func TestIssue700LastCloseAttemptSemantics(t *testing.T) {
	a := NewApp()
	if a.lastCloseAttempt.Load() != nil {
		t.Fatal("fresh App should have nil lastCloseAttempt")
	}
	now := time.Now()
	a.lastCloseAttempt.Store(&now)
	got := a.lastCloseAttempt.Load()
	if got == nil || !got.Equal(now) {
		t.Fatalf("Load after Store mismatch: got %v want %v", got, now)
	}
	a.lastCloseAttempt.Store(nil)
	if a.lastCloseAttempt.Load() != nil {
		t.Fatal("Store(nil) should clear lastCloseAttempt")
	}
}
