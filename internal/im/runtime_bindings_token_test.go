package im

import (
	"errors"
	"testing"
)

// failSaveStore wraps a store whose Save always errors (#520 scenario).
// Embeds the BindingStore interface so all methods (ListByWorkspace,
// ListByAdapter, BindExclusive, UpdateSessionID) are promoted from inner;
// a named field would NOT promote them.
type failSaveStore struct{ BindingStore }

func (s *failSaveStore) Save(b ChannelBinding) error     { return errors.New("disk full") }
func (s *failSaveStore) Delete(w, a string) error        { return s.BindingStore.Delete(w, a) }
func (s *failSaveStore) List() ([]ChannelBinding, error) { return s.BindingStore.List() }

// #520: UpdateBindingContextToken must not panic or silently mislead when the
// store Save fails — the error is now logged (previously swallowed, followed
// by an unconditional "persisted" log).
func TestUpdateBindingContextToken_SaveErrorNoPanic(t *testing.T) {
	mgr := NewManager()
	mgr.SetBindingStore(&failSaveStore{BindingStore: NewMemoryBindingStore()})
	mgr.currentBindings["wechat"] = &ChannelBinding{Workspace: "/w", Adapter: "wechat"}

	mgr.UpdateBindingContextToken("wechat", "tok-123")

	b := mgr.currentBindings["wechat"]
	if b.ContextToken != "tok-123" {
		t.Errorf("in-memory token not updated: %q", b.ContextToken)
	}
	if b.ContextTokenUpdatedAt.IsZero() {
		t.Error("ContextTokenUpdatedAt not set")
	}
}
