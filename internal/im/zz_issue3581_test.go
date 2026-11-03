package im

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

func TestIssue3581_UnbindInListWindowNoPanic(t *testing.T) {
	m := NewManager()
	mem := NewMemoryBindingStore()
	if err := mem.Save(ChannelBinding{Workspace: "/w", Platform: PlatformQQ, Adapter: "qq", LastSessionID: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := &issue3581Store{MemoryBindingStore: mem}
	m.bindingStore = store
	m.session = &SessionBinding{SessionID: "s-old", Workspace: "/w"}

	store.onList = func() {
		m.mu.Lock()
		m.session = nil
		m.mu.Unlock()
	}

	m.claimUnclaimedBindings("s-new")

	got, err := mem.ListByWorkspace("/w")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 1 && got[0].LastSessionID != "" {
		t.Fatalf("unbound session must not claim bindings, got LastSessionID=%q", got[0].LastSessionID)
	}
}
