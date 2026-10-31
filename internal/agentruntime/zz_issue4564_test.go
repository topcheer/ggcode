package agentruntime

// #4564: an event that resolved the message ID before a concurrent rollover
// could still be pushed AFTER the rollover's PushTextDone - the append landed
// on a finalized message (projection reorder: text after done). The fix
// serializes (ID resolution + push) against (finalize mark + Done) on a
// dedicated pushMu, and a publisher that observes a finalized ID rebinds to a
// fresh message.

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// State machine: after rollover finalizes the current ID, the next text push
// must NOT reuse that ID.
func TestIssue4564_LatePublisherRebindsAfterRollover(t *testing.T) {
	h := NewTunnelHost()
	broker := tunnel.NewBroker(nil)

	// Publisher A streams one chunk: allocates msg-1.
	h.markActive()
	h.pushTextEvent(broker, "hello")

	h.mu.Lock()
	first := h.currentMsgID
	h.mu.Unlock()
	if first == "" {
		t.Fatal("first push should have allocated a message ID")
	}

	// Concurrent rollover finalizes it (ToolCallDone-shaped: startNew=true).
	h.rollover(broker, true)

	h.mu.Lock()
	marked := h.finalized[first]
	h.mu.Unlock()
	if !marked {
		t.Fatalf("rollover must mark %q finalized before Done goes out (#4564)", first)
	}

	// Late publisher B (resolved before the rollover, scheduled after):
	// its push must land on a FRESH message, not the finalized one.
	h.markActive()
	h.pushTextEvent(broker, "late")
	h.mu.Lock()
	second := h.currentMsgID
	h.mu.Unlock()
	if second == first {
		t.Fatalf("#4564: late push reused the finalized ID %q - text appended to a done message", second)
	}
}

// Hammer version: concurrent publishers + rollovers must never push onto an
// ID that is marked finalized (run with -race for the lock discipline).
func TestIssue4564_ConcurrentPushAndRolloverNoDoneAfterAppend(t *testing.T) {
	h := NewTunnelHost()
	broker := tunnel.NewBroker(nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { // publisher
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.markActive()
				h.pushTextEvent(broker, "x")
			}
		}()
		go func() { // rollover driver
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.rollover(broker, true)
			}
		}()
	}
	wg.Wait()

	// Invariant: whatever ID is current must not be marked finalized.
	h.mu.Lock()
	cur, marked := h.currentMsgID, h.finalized[h.currentMsgID]
	h.mu.Unlock()
	if cur != "" && marked {
		t.Fatalf("#4564: current ID %q is marked finalized - a later push would append to a done message", cur)
	}
}
