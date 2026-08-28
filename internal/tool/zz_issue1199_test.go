package tool

import (
	"sync"
	"testing"
	"time"
)

// TestIssue1199_RaceSafety verifies that rgPath atomic.Value is race-free.
// This test would fail under -race if rgPath were a plain string with
// concurrent reads (rgAvailable) and writes (installRG).
func TestIssue1199_RaceSafety(t *testing.T) {
	// Reset state for clean test
	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)

	// Launch multiple concurrent calls to rgAvailable
	// In the buggy version, this would trigger a data race:
	// - goroutine 1 reads rgPath (string)
	// - installRG writes rgPath (no sync)
	// - goroutine 2 reads rgPath (torn read possible)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rgAvailable()
		}()
	}
	wg.Wait()
}

// TestIssue1199_TimestampGating verifies that concurrent calls during
// recheck window don't all trigger LookPath (only first one does).
func TestIssue1199_TimestampGating(t *testing.T) {
	// Reset state - ensure path is not cached
	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)

	origWindow := rgRecheckWindow
	defer func() { rgRecheckWindow = origWindow }()

	// Use a longer window to ensure concurrent calls fall within it
	rgRecheckWindow = 1 * time.Second

	// We verify behavior by checking that rgLastCheck doesn't change rapidly.
	// The timestamp gating should prevent multiple LookPath calls.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rgAvailable()
		}()
	}
	wg.Wait()

	// After all concurrent calls, timestamp should be set only once
	// (or not set if rg was already found - that's OK)
	lastCheck := rgLastCheck.Load()

	// Immediate concurrent calls should not update timestamp
	wg = sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rgAvailable()
		}()
	}
	wg.Wait()

	// Timestamp should remain unchanged (within window)
	newLastCheck := rgLastCheck.Load()
	if newLastCheck != lastCheck {
		t.Error("expected rgLastCheck to remain unchanged within recheck window")
	}
}

// TestIssue1199_FastPathUnchanged verifies that when rg is available,
// the fast path behavior is identical (no re-probing).
func TestIssue1199_FastPathUnchanged(t *testing.T) {
	// Set rgPath as if already detected
	path := "/existing/rg"
	rgPath.Store(&path)
	rgLastCheck.Store(time.Now().Unix())

	// Save original window
	origWindow := rgRecheckWindow
	defer func() { rgRecheckWindow = origWindow }()

	// Set to zero to force recheck if path were nil
	rgRecheckWindow = 0

	// Should return true immediately without calling LookPath
	if !rgAvailable() {
		t.Fatal("expected rgAvailable to return true with cached path")
	}

	// Verify timestamp didn't change (fast path)
	firstCheck := rgLastCheck.Load()
	_ = rgAvailable()
	secondCheck := rgLastCheck.Load()
	if firstCheck != secondCheck {
		t.Error("expected fast path to skip re-probe when path is cached")
	}
}

// TestIssue1199_InstallRGNoPanic verifies that installRG doesn't panic
// even when no package manager is available.
func TestIssue1199_InstallRGNoPanic(t *testing.T) {
	// Reset state
	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)
	rgTrying.Store(false)

	// installRG should return early without panic
	// In the buggy version, this silently did nothing
	// In the fixed version, we at least don't panic
	installRG()

	// Verify rgTrying is reset
	if rgTrying.Load() {
		t.Error("expected rgTrying to be reset after installRG")
	}
}

// TestIssue1199_AtomicStoreLoad verifies atomic.Value store/load semantics
// for *string type used by rgPath.
func TestIssue1199_AtomicStoreLoad(t *testing.T) {
	// Reset state
	rgPath.Store((*string)(nil))

	// Store a path string
	path := "/test/path/rg"
	rgPath.Store(&path)

	// Load and verify
	ptr := rgPath.Load()
	if ptr == nil {
		t.Fatal("expected non-nil pointer after store")
	}

	loadedPath := ptr.(*string)
	if *loadedPath != path {
		t.Errorf("expected %s, got %s", path, *loadedPath)
	}
}

// TestIssue1199_ReprobeAfterWindow verifies that after the recheck window,
// rgAvailable will reattempt detection.
func TestIssue1199_ReprobeAfterWindow(t *testing.T) {
	// Save original window and restore after test
	origWindow := rgRecheckWindow
	defer func() { rgRecheckWindow = origWindow }()

	// Reset state (but keep rgPath as-is if already set)
	rgLastCheck.Store(0)

	// Set very short window for testing (50ms)
	rgRecheckWindow = 50 * time.Millisecond

	// First call sets the timestamp (unless path was already cached)
	_ = rgAvailable()
	firstCheck := rgLastCheck.Load()

	// If rg was already cached, firstCheck may be 0 - that's OK
	if firstCheck > 0 {
		// Immediate retry should return cached result (within window)
		_ = rgAvailable()
		secondCheck := rgLastCheck.Load()
		if secondCheck != firstCheck {
			t.Error("expected timestamp to remain unchanged within recheck window")
		}

		// Wait for window to expire
		time.Sleep(rgRecheckWindow + 10*time.Millisecond)

		// After window, should retry and update timestamp
		_ = rgAvailable()
		thirdCheck := rgLastCheck.Load()
		if thirdCheck <= secondCheck {
			t.Error("expected timestamp to update after recheck window expires")
		}
	}
	// If firstCheck == 0, it means rg was cached from before - skip rest
}
