package im

import (
	"errors"
	"testing"
)

type failSaveStore struct{ BindingStore }

func (s *failSaveStore) Save(b ChannelBinding) error     { return errors.New("disk full") }
func (s *failSaveStore) Delete(w, a string) error        { return s.BindingStore.Delete(w, a) }
func (s *failSaveStore) List() ([]ChannelBinding, error) { return s.BindingStore.List() }

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
