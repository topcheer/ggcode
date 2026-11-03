package tui

import "testing"

func TestCancelActiveRunNoPoisonWhenUncancellable1768(t *testing.T) {
	m := newTestModel()
	m.setLoading(true)
	m.cancelFunc = nil
	m.shellOwnedLoading = false

	m.cancelActiveRun()

	if m.runCanceled {
		t.Fatal("runCanceled must stay false when there is no cancellable run (cross-run poisoning)")
	}

	m.cancelFunc = func() {}
	m.cancelActiveRun()
	if !m.runCanceled {
		t.Fatal("runCanceled must be set when a cancellable run is active")
	}
}
