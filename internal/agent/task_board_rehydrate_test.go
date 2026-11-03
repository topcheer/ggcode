package agent

import (
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

type fakePostCompactNoteSetter struct {
	ctxpkg.ContextManager
	registered func() string
}

func (f *fakePostCompactNoteSetter) SetPostCompactNoteProvider(fn func() string) {
	f.registered = fn
}

func TestSetTaskBoardSnapshotterWiresContextManager(t *testing.T) {
	a := &Agent{}
	fake := &fakePostCompactNoteSetter{}
	a.contextManager = fake

	calls := 0
	a.SetTaskBoardSnapshotter(func() string {
		calls++
		return "Task board: 1 pending"
	})
	if fake.registered == nil {
		t.Fatal("expected provider registered on context manager")
	}
	if got := fake.registered(); got != "Task board: 1 pending" || calls != 1 {
		t.Fatalf("unexpected provider behavior: got=%q calls=%d", got, calls)
	}
}

func TestSetTaskBoardSnapshotterNoCapabilityIsNoOp(t *testing.T) {
	a := &Agent{}
	a.SetTaskBoardSnapshotter(func() string { return "x" })
	a.SetTaskBoardSnapshotter(nil)
	(*Agent)(nil).SetTaskBoardSnapshotter(nil)
}
