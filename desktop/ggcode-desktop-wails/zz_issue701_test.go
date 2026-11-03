package main

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// #701: enqueueUIEvent must never block — if the 4096 buffer is full (dead
// or slow consumer), the event is dropped instead of freezing the emitter.
func TestIssue701EnqueueUIEventNonBlockingOnFullBuffer(t *testing.T) {
	debug.Init()
	a := NewApp()
	a.startEventLoop()
	if a.streamEvents == nil {
		t.Fatal("startEventLoop should create streamEvents")
	}

	a2 := NewApp()
	a2.streamOnce.Do(func() {})
	a2.streamEvents = make(chan uiEvent, 2)

	done := make(chan struct{})
	go func() {
		defer close(done)

		for i := 0; i < 100; i++ {
			a2.enqueueUIEvent("test:event", i)
		}
	}()

	select {
	case <-done:

	case <-time.After(3 * time.Second):
		t.Fatal("enqueueUIEvent blocked on a full buffer — emitter deadlock (#701)")
	}
}

// #701: the consumer loop must keep draining after dispatch — each event is
// dispatched through safego.Run, which recovers panics per-event. (a.ctx
// cannot be set to a plain context in tests: Wails' EventsEmit log.Fatal's
// on a non-Wails context, so with ctx==nil the consumer skips emit and still
// drains — which is exactly what this asserts, plus a direct proof that the
// per-event safego.Run guard recovers a panic without killing the process.)
func TestIssue701EventLoopSurvivesPanickingEvent(t *testing.T) {
	debug.Init()

	panicked := make(chan struct{})
	safego.Run("zz-test-panic", func() {
		defer close(panicked)
		panic("boom")
	})
	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatal("panicking closure did not run")
	}

	a := NewApp()
	a.startEventLoop()

	deadline := time.After(3 * time.Second)
	for i := 0; i < 50; i++ {
		a.enqueueUIEvent("test:drain", i)
	}
	for {
		if len(a.streamEvents) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("event loop did not drain the buffer (len=%d) — consumer dead?", len(a.streamEvents))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
