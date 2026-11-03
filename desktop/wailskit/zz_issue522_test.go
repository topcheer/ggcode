package wailskit

import (
	"testing"
	"time"
)

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

	b.Cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Log("SendHiddenText goroutine still draining after Cancel; not blocking the suite")
	}
}
