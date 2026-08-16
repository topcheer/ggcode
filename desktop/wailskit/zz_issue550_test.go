package wailskit

// Feature tests for issue #550 E1 (ChatBridge): generation-bypass event
// leaks from superseded runs — appendLiveError, finishRun's run_done, and
// ClearCurrentSession's missing busy guard.

import (
	"encoding/json"
	"testing"
)

// E1: ClearCurrentSession must refuse while a run is active instead of
// silently installing a zombie run against the next session.
func TestIssue550_ClearCurrentSessionRefusedWhileBusy(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("NewChatBridge unavailable in test env: %v", err)
	}
	cancelled := false
	b.mu.Lock()
	b.cancel = func() { cancelled = true } // simulate an active run
	b.finished = false
	b.mu.Unlock()

	before := b.currentRunGeneration()
	if err := b.ClearCurrentSession(); err == nil {
		t.Fatal("E1: ClearCurrentSession must refuse while a run is active")
	}
	if b.currentRunGeneration() != before {
		t.Fatal("E1: refused clear must not bump the generation")
	}

	// After Cancel (b.cancel nilled in its critical section), clearing
	// proceeds — the DeleteSession / StartNewSession shape.
	b.Cancel()
	if !cancelled {
		t.Fatal("E1: Cancel must invoke the installed cancel func")
	}
	if err := b.ClearCurrentSession(); err != nil {
		t.Fatalf("E1: clear after cancel should succeed, got: %v", err)
	}
}

// E1: a superseded run's late error must not leak into the new session's
// live history (previously bypassed the emitIfCurrent generation guard).
func TestIssue550_AppendLiveErrorGatedByGeneration(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("NewChatBridge unavailable in test env: %v", err)
	}
	gen := b.currentRunGeneration()
	b.appendLiveErrorIfCurrent(gen, "boom")
	b.mu.Lock()
	n1 := len(b.liveHistory)
	b.runGeneration++ // supersede: session cleared / newer run started
	b.mu.Unlock()
	b.appendLiveErrorIfCurrent(gen, "stale leak")
	b.mu.Lock()
	n2 := len(b.liveHistory)
	b.mu.Unlock()
	if n1 != 1 {
		t.Fatalf("E1: current-generation error must be recorded, n1=%d", n1)
	}
	if n2 != 1 {
		t.Fatalf("E1: stale-generation error must be dropped, n2=%d", n2)
	}
}

// E1: finishRun from a SUPERSEDED run must not emit run_done (it would
// clear the new run's frontend busy state), while a current run still does.
func TestIssue550_FinishRunRunDoneSuppressedWhenSuperseded(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("NewChatBridge unavailable in test env: %v", err)
	}
	var events []string
	b.OnStreamEvent = func(eventType string, _ json.RawMessage) {
		events = append(events, eventType)
	}

	// Simulate a run start (generation N owned by activeRunGen), then
	// supersede it WITHOUT finishing.
	b.mu.Lock()
	b.runGeneration++
	b.activeRunGen = b.runGeneration
	b.finished = false
	b.mu.Unlock()
	b.mu.Lock()
	b.runGeneration++
	b.mu.Unlock()

	b.finishRun(nil)
	for _, e := range events {
		if e == "run_done" {
			t.Fatal("E1: superseded run must not emit run_done")
		}
	}

	// Control: a current (non-superseded) run still emits run_done.
	b.mu.Lock()
	b.runGeneration++
	b.activeRunGen = b.runGeneration
	b.finished = false
	b.mu.Unlock()
	b.finishRun(nil)
	found := false
	for _, e := range events {
		if e == "run_done" {
			found = true
		}
	}
	if !found {
		t.Fatal("E1: current run must still emit run_done")
	}
}

// E1: StartNewSession must cancel an active run before clearing — the
// mid-run "New Session" zombie entry point.
func TestIssue550_StartNewSessionCancelsActiveRun(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("NewChatBridge unavailable in test env: %v", err)
	}
	cancelled := false
	b.mu.Lock()
	b.cancel = func() { cancelled = true }
	b.finished = false
	b.mu.Unlock()

	// ensureSession may fail in a bare test bridge; the clear must still
	// have happened (i.e., not been refused for business).
	_, _ = b.StartNewSession()
	if !cancelled {
		t.Fatal("E1: StartNewSession must cancel an active run before clearing")
	}
}
