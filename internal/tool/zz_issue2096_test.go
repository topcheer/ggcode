package tool

// #2096 bug A regression: the background profile-GC goroutine had no
// termination path - Close() did not stop it, so every browser-using
// session left an immortal goroutine pinning its Browser (NewBrowser
// re-registers per agent build; long-lived hosts accumulated them).
// Close must close the stop signal and be idempotent.

import (
	"testing"
	"time"
)

func TestCloseStopsProfileGC(t *testing.T) {
	b := NewBrowser()
	b.startBrowserProfileGC()
	// Give the goroutine a moment to enter its select loop.
	time.Sleep(50 * time.Millisecond)

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-b.gcStop:
		// signal closed - the GC loop returns on its next select.
	default:
		// Allow a tiny window; the close is synchronous though.
		select {
		case <-b.gcStop:
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not close the GC stop signal - goroutine leaks (#2096 bug A)")
		}
	}
	// Idempotent: a second Close must not panic on double-close.
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Direct &Browser{} construction (test pattern) has a nil gcStop: Close
// must skip the stop gracefully and stay non-panicking.
func TestCloseNilGCStopSafe(t *testing.T) {
	b := &Browser{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close on zero-value Browser: %v", err)
	}
}
