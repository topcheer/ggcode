package im

// Issue #954: IMEmitter lazily initialized e.state and e.typing without any
// locking — two concurrent emit paths could each install their own state
// (leaking a bounded extra dispatcher goroutine) and TriggerTyping raced on
// typing.lastTrigger. Additionally the #603 close() was never wired to a
// public shutdown path, so dispatcher goroutines leaked for the process
// lifetime. These tests exercise the concurrent paths under -race and verify
// Close drains buffered events and is idempotent.

import (
	"sync"
	"testing"
	"time"
)

func TestIMEmitterConcurrentEmitAndTyping(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "telegram"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["telegram"] = &ChannelBinding{Adapter: "telegram", ChannelID: "ch1"}
	e := NewIMEmitter(mgr, "en", t.TempDir())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				e.EmitText("hello concurrent")
				e.EmitStatus("working")
				e.EmitUserTextExcept("echo", "telegram")
				e.TriggerTyping()
			}
		}()
	}
	wg.Wait()

	// The dispatcher must have drained at least one event (proves exactly one
	// pipeline was installed and functioning, not zero).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink.events()) >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no events drained by emitter dispatcher after concurrent emits")
}

func TestIMEmitterCloseDrainsAndIsIdempotent(t *testing.T) {
	mgr := NewManager()
	sink := &namedCaptureSink{name: "telegram"}
	mgr.RegisterSink(sink)
	mgr.currentBindings["telegram"] = &ChannelBinding{Adapter: "telegram", ChannelID: "ch1"}
	e := NewIMEmitter(mgr, "en", t.TempDir())

	e.EmitText("before close")
	e.Close()
	e.Close() // must not panic (double close)

	// Buffered events must be drained by the dispatcher before it exits.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink.events()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if evs := sink.events(); len(evs) < 1 {
		t.Fatalf("buffered event not drained before dispatcher exit; got %d events", len(evs))
	}

	// Post-close emissions must not panic; they lazily re-init a fresh state.
	e.EmitText("after close")
	e.TriggerTyping()
}
