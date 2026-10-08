package im

// #3581 probe: the second locked section of claimUnclaimedBindings must
// not dereference m.session - the ListByWorkspace window between the two
// locks is UNLOCKED, and a concurrent UnbindSession sets m.session=nil
// there. Simulate that window with a store whose ListByWorkspace unbinds
// (the exact interleaving the issue describes).

import "testing"

type issue3581Store struct {
	*MemoryBindingStore
	onList func()
}

func (s *issue3581Store) ListByWorkspace(workspace string) ([]ChannelBinding, error) {
	if s.onList != nil {
		s.onList()
	}
	return s.MemoryBindingStore.ListByWorkspace(workspace)
}

// (Happy-path claim coverage already exists in the runtime tests.)

func TestIssue3581_UnbindInListWindowNoPanic(t *testing.T) {
	m := NewManager()
	mem := NewMemoryBindingStore()
	if err := mem.Save(ChannelBinding{Workspace: "/w", Platform: PlatformQQ, Adapter: "qq", LastSessionID: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := &issue3581Store{MemoryBindingStore: mem}
	m.bindingStore = store
	m.session = &SessionBinding{SessionID: "s-old", Workspace: "/w"}

	// The unbind lands INSIDE the unlocked ListByWorkspace window - after
	// the first lock's snapshot, before the second lock's use.
	store.onList = func() {
		m.mu.Lock()
		m.session = nil // what UnbindSession does in that window
		m.mu.Unlock()
	}

	// Must return cleanly; the pre-fix code panicked on m.session.Workspace.
	m.claimUnclaimedBindings("s-new")

	// And the binding must NOT be claimed for a session that just unbound.
	got, err := mem.ListByWorkspace("/w")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 1 && got[0].LastSessionID != "" {
		t.Fatalf("unbound session must not claim bindings, got LastSessionID=%q", got[0].LastSessionID)
	}
}
