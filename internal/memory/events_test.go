package memory

import (
	"testing"
	"time"
)

func TestSetEventSink_SaveEmitsEvent(t *testing.T) {
	am := NewProjectAutoMemory(t.TempDir())
	events := make(chan ChangeEvent, 64)
	am.SetEventSink("project", events)

	if err := am.SaveMemoryWithSource("build-process", "run make verify-ci", "save_memory"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Scope != "project" || ev.Op != "save" || ev.Key != "build-process" || ev.Source != "save_memory" {
			t.Fatalf("unexpected event: %+v", ev)
		}
		if ev.At.IsZero() {
			t.Fatal("event time not set")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("save did not emit a change event")
	}
}

func TestSetEventSink_DeleteEmitsEvent(t *testing.T) {
	am := NewProjectAutoMemory(t.TempDir())
	if err := am.SaveMemory("stale-fact", "old content"); err != nil {
		t.Fatal(err)
	}
	events := make(chan ChangeEvent, 64)
	am.SetEventSink("global", events)

	if err := am.DeleteMemory("stale-fact"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		if ev.Op != "delete" || ev.Key != "stale-fact" || ev.Source != "delete_memory" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delete did not emit a change event")
	}
}

func TestSetEventSink_FullSinkDropsWithoutBlocking(t *testing.T) {
	am := NewProjectAutoMemory(t.TempDir())
	events := make(chan ChangeEvent, 1) // deliberately tiny
	am.SetEventSink("project", events)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10; i++ {
			if err := am.SaveMemory("churn-key", "content"); err != nil {
				t.Errorf("save %d blocked into failure: %v", i, err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("saves blocked on a full event sink")
	}
	if len(events) > 1 {
		t.Fatalf("sink over capacity: %d buffered", len(events))
	}
}

func TestSetEventSink_NilSinkAndClear(t *testing.T) {
	am := NewProjectAutoMemory(t.TempDir())
	// No sink registered: must not panic or block.
	if err := am.SaveMemory("plain", "content"); err != nil {
		t.Fatal(err)
	}
	events := make(chan ChangeEvent, 64)
	am.SetEventSink("project", events)
	am.SetEventSink("", nil) // clear
	if err := am.SaveMemory("plain", "content2"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-events:
		t.Fatalf("cleared sink still received event: %+v", ev)
	default:
	}
}
