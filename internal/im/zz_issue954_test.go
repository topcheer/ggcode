package im

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
	e.Close()

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

	e.EmitText("after close")
	e.TriggerTyping()
}
