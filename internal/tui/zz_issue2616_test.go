package tui

import (
	"testing"
)

func TestNewModelKnightTasksPreBuilt(t *testing.T) {
	m := NewModel(nil, nil)
	if m.knightTasks == nil {
		t.Fatal("NewModel must pre-build knightTasks (#2616): nil registry means cancelKnightTasks can never cancel anything")
	}

	m2 := m
	if m2.knightTasks != m.knightTasks {
		t.Fatal("knightTasks must be pointer-shared across Model copies; a lazy init on the copy would write a dead registry (#2616)")
	}
}

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

	m.cancelKnightTasks()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("cancelKnightTasks must cancel the registered task's context (#2616: registrations used to land in a dead copy and this never fired)")
	}

	m.releaseKnightTask(handle)
}
