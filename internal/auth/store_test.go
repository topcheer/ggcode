package auth

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

func TestStoreConcurrentSavesNoLostUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider_auth.json")
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := NewStore(path)
			info := &Info{
				ProviderID:  fmt.Sprintf("provider-%d", i),
				Type:        "oauth",
				AccessToken: fmt.Sprintf("token-%d", i),
			}
			if err := store.Save(info); err != nil {
				t.Errorf("Save() from goroutine %d error = %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	reader := NewStore(path)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("provider-%d", i)
		info, err := reader.Load(id)
		if err != nil {
			t.Fatalf("Load(%s) error = %v", id, err)
		}
		if info == nil {
			t.Fatalf("lost update: provider %s missing after concurrent saves", id)
		}
		if want := fmt.Sprintf("token-%d", i); info.AccessToken != want {
			t.Fatalf("provider %s: token = %q, want %q", id, info.AccessToken, want)
		}
	}
}

func TestStoreSaveNoTmpResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider_auth.json")
	store := NewStore(path)
	if err := store.Save(&Info{ProviderID: ProviderAnthropic, Type: "oauth", AccessToken: "at"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "provider_auth.json.tmp*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	leftover := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e, ".lock") {
			continue
		}
		leftover = append(leftover, filepath.Base(e))
	}
	if len(leftover) > 0 {
		t.Fatalf("temp file residue after Save: %v", leftover)
	}
}
