package tui

import "testing"

// #1768 case 1+2: cancelActiveRun during an uncancellable loading
// window (e.g. /compact: Background ctx, cancelFunc nil) must NOT
// poison runCanceled - the next normal run would be treated as
// cancelled (persist/metrics/drain skipped).
func TestCancelActiveRunNoPoisonWhenUncancellable1768(t *testing.T) {
	m := newTestModel()
	m.setLoading(true) // /compact-style loading
	m.cancelFunc = nil // Background ctx - nothing to cancel
	m.shellOwnedLoading = false

	m.cancelActiveRun()

	if m.runCanceled {
		t.Fatal("runCanceled must stay false when there is no cancellable run (cross-run poisoning)")
	}

	// A cancellable run still poisons normally.
	m.cancelFunc = func() {}
	m.cancelActiveRun()
	if !m.runCanceled {
		t.Fatal("runCanceled must be set when a cancellable run is active")
	}
}
