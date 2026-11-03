package main

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

func TestIssue706_InteractiveEventNotDroppedOnFullBuffer(t *testing.T) {
	debug.Init()
	a := NewApp()
	a.streamOnce.Do(func() {})
	a.streamEvents = make(chan uiEvent, 1)

	a.enqueueUIEvent("chat:stream", map[string]string{"t": "token"})

	a.enqueueUIEvent("chat:stream", map[string]string{"t": "dropped"})
	if len(a.streamEvents) != 1 {
		t.Fatalf("stream data should still be droppable on full buffer, len=%d", len(a.streamEvents))
	}

	for _, name := range []string{"ask_user:request", "approval:request", "ask_user:cancel", "approval:cancel", "pending_consumed"} {
		done := make(chan struct{})
		go func(n string) {
			defer close(done)
			a.enqueueUIEvent(n, nil)
		}(name)
		select {
		case <-done:

		case <-time.After(3 * time.Second):
			t.Fatalf("interactive event %q blocked or died on full buffer — no lossless bypass (#706)", name)
		}

		if ev := <-a.streamEvents; ev.name != "chat:stream" {
			t.Fatalf("unexpected event %q in buffer", ev.name)
		}
		a.enqueueUIEvent("chat:stream", nil)
	}
}

func TestIssue706_InteractiveUIEventClassification(t *testing.T) {
	interactive := []string{
		"ask_user:request", "approval:request",
		"ask_user:cancel", "approval:cancel", "pending_consumed",
	}
	for _, n := range interactive {
		if !interactiveUIEvent(n) {
			t.Errorf("interactiveUIEvent(%q) = false, want true", n)
		}
	}
	droppable := []string{"chat:stream", "run_done", "error", "session:changed", "log:append"}
	for _, n := range droppable {
		if interactiveUIEvent(n) {
			t.Errorf("interactiveUIEvent(%q) = true, want false (regenerable/non-interactive)", n)
		}
	}
}

func TestIssue706_RequestAndCancelBothSurviveFullBuffer(t *testing.T) {
	debug.Init()
	a := NewApp()
	a.streamOnce.Do(func() {})
	a.streamEvents = make(chan uiEvent, 1)
	a.enqueueUIEvent("chat:stream", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.enqueueUIEvent("ask_user:request", map[string]string{"requestID": "r1"})
		a.enqueueUIEvent("ask_user:cancel", map[string]string{"requestID": "r1"})
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("request+cancel pair did not both survive a full buffer — agent would hang on the no-timeout ask (#706)")
	}
}
