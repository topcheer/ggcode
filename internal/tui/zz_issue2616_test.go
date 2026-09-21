package tui

import (
	"testing"
)

// #2616 pin tests (reviewer-requested on reopen): the fix itself passed
// review; what was missing were assertions so the "lazy nil-init writes a
// dead value-receiver copy" pattern (#1737 -> #2616, twice now) cannot
// silently return. A nil-guard here LOOKS harmless but sends every
// registration to a dead copy and shutdown then cancels nothing.

// TestNewModelKnightTasksPreBuilt pins the constructor guarantee: the
// registry must exist after NewModel and must be the SAME pointer across
// Model copies (Update is a value receiver - a fresh lazy init on any
// copy would split the registry in two and reproduce #2616).
func TestNewModelKnightTasksPreBuilt(t *testing.T) {
	m := NewModel(nil, nil)
	if m.knightTasks == nil {
		t.Fatal("NewModel must pre-build knightTasks (#2616): nil registry means cancelKnightTasks can never cancel anything")
	}
	// Value-copy semantics: a copy shares the registry pointer.
	m2 := m // exactly what Update does to the model
	if m2.knightTasks != m.knightTasks {
		t.Fatal("knightTasks must be pointer-shared across Model copies; a lazy init on the copy would write a dead registry (#2616)")
	}
}

// TestKnightRegisterCancelRoundTrip pins the full path the fix protects:
// register on the authoritative model, cancel via a (copied) model as the
// shutdown path sees it, and the task's context must be cancelled.
func TestKnightRegisterCancelRoundTrip(t *testing.T) {
	m := NewModel(nil, nil)
	ctx, handle := m.registerKnightTask()
	if ctx == nil || handle == nil {
		t.Fatal("registerKnightTask must return a context and handle")
	}
	select {
	case <-ctx.Done():
		t.Fatal("freshly registered task must not be cancelled yet")
	default:
	}
	// The shutdown path runs on whatever model copy it holds; with the
	// pre-built shared registry this must still reach our handle.
	m.cancelKnightTasks()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("cancelKnightTasks must cancel the registered task's context (#2616: registrations used to land in a dead copy and this never fired)")
	}
	// release after cancel is a no-op but must not panic on the empty set.
	m.releaseKnightTask(handle)
}
