package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSaveLoadDelete(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "provider_auth.json"))
	info := &Info{
		ProviderID:   ProviderGitHubCopilot,
		Type:         "oauth",
		AccessToken:  "token-1",
		RefreshToken: "token-1",
	}
	if err := store.Save(info); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(ProviderGitHubCopilot)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "token-1" {
		t.Fatalf("expected saved token, got %#v", loaded)
	}
	if err := store.Delete(ProviderGitHubCopilot); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	loaded, err = store.Load(ProviderGitHubCopilot)
	if err != nil {
		t.Fatalf("Load() after delete error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected provider to be deleted, got %#v", loaded)
	}
}

func TestStoreHasUsableToken(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "provider_auth.json"))
	if err := store.Save(&Info{
		ProviderID:  ProviderGitHubCopilot,
		Type:        "oauth",
		AccessToken: "token-1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	ok, err := store.HasUsableToken(ProviderGitHubCopilot)
	if err != nil {
		t.Fatalf("HasUsableToken() error = %v", err)
	}
	if !ok {
		t.Fatal("expected token to be usable")
	}
}
