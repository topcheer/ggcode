package wailskit

import (
	"testing"
	"time"
)

// TestHiddenRunStartBumpsGenerationAndTurn (#522): the hidden-run start
// critical section must bump runGeneration and open a desktop turn —
// previously both were SendContent-only, so a cancelled run's tail-draining
// events passed the emitIfCurrent guard of the next text/hidden run (stale
// events leaked into the new run) and run_done carried a stale/empty turn_id.
//
// The probe observes the run-start critical section only: SendHiddenText runs
// in a goroutine (its agent-init tail may block on provider I/O in the full
// suite), the generation bump + desktop turn are asserted as soon as they are
// observable, then Cancel() unwinds the run so the test stays bounded.
func TestHiddenRunStartBumpsGenerationAndTurn(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge unavailable in test env: %v", err)
	}

	before := b.currentRunGeneration()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.SendHiddenText("probe")
	}()

	// The bump + turn start happen in the first critical section of the run,
	// observable within milliseconds of the call.
	var after uint64
	turnID := ""
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		after = b.currentRunGeneration()
		b.mu.Lock()
		tid := b.desktopTurnID
		b.mu.Unlock()
		if after > before && tid != "" {
			turnID = tid
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if after != before+1 {
		t.Fatalf("hidden run must bump runGeneration exactly once: before=%d after=%d", before, after)
	}
	if turnID == "" {
		t.Fatal("hidden run must start a desktop turn (#514 parity) — desktopTurnID empty")
	}

	// Unwind the run so the goroutine cannot block the suite on provider I/O.
	b.Cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Log("SendHiddenText goroutine still draining after Cancel; not blocking the suite")
	}
}
