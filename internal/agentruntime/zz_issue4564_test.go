package agentruntime

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestIssue4564_LatePublisherRebindsAfterRollover(t *testing.T) {
	h := NewTunnelHost()
	broker := tunnel.NewBroker(nil)

	h.markActive()
	h.pushTextEvent(broker, "hello")

	h.mu.Lock()
	first := h.currentMsgID
	h.mu.Unlock()
	if first == "" {
		t.Fatal("first push should have allocated a message ID")
	}

	h.rollover(broker, true)

	h.mu.Lock()
	marked := h.finalized[first]
	h.mu.Unlock()
	if !marked {
		t.Fatalf("rollover must mark %q finalized before Done goes out (#4564)", first)
	}

	h.markActive()
	h.pushTextEvent(broker, "late")
	h.mu.Lock()
	second := h.currentMsgID
	h.mu.Unlock()
	if second == first {
		t.Fatalf("#4564: late push reused the finalized ID %q - text appended to a done message", second)
	}
}

func TestIssue4564_ConcurrentPushAndRolloverNoDoneAfterAppend(t *testing.T) {
	h := NewTunnelHost()
	broker := tunnel.NewBroker(nil)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.markActive()
				h.pushTextEvent(broker, "x")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h.rollover(broker, true)
			}
		}()
	}
	wg.Wait()

	h.mu.Lock()
	cur, marked := h.currentMsgID, h.finalized[h.currentMsgID]
	h.mu.Unlock()
	if cur != "" && marked {
		t.Fatalf("#4564: current ID %q is marked finalized - a later push would append to a done message", cur)
	}
}
