package agent

import (
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

// fakePostCompactNoteSetter embeds the ContextManager interface so it
// satisfies the full interface while recording the registered provider.
type fakePostCompactNoteSetter struct {
	ctxpkg.ContextManager
	registered func() string
}

func (f *fakePostCompactNoteSetter) SetPostCompactNoteProvider(fn func() string) {
	f.registered = fn
}

// TestSetTaskBoardSnapshotterWiresContextManager verifies the snapshotter
// is registered on a capable context manager and is invoked lazily.
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

// TestSetTaskBoardSnapshotterNoCapabilityIsNoOp verifies no panic when the
// context manager lacks the optional capability or inputs are nil.
func TestSetTaskBoardSnapshotterNoCapabilityIsNoOp(t *testing.T) {
	a := &Agent{} // contextManager nil: type assertion fails safely
	a.SetTaskBoardSnapshotter(func() string { return "x" })
	a.SetTaskBoardSnapshotter(nil)
	(*Agent)(nil).SetTaskBoardSnapshotter(nil)
}
