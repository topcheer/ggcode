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

// TestStoreConcurrentSavesNoLostUpdate is the #1505 case 3 regression:
// DefaultStore() hands out a fresh Store per call, so per-instance mutexes
// never serialized anything - N concurrent load-modify-write cycles on the
// same path dropped each other's entries. Every instance sharing a path
// must now serialize through one mutex (plus the cross-process flock), so
// N distinct providers saved concurrently must all survive.
func TestStoreConcurrentSavesNoLostUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider_auth.json")
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// A separate instance per goroutine is the point: this is what
			// DefaultStore() callers actually get.
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

// TestStoreSaveNoTmpResidue pins the #1505 case 3 scratch-file fix: every
// save must consume its uniquely-named temp file via rename, leaving no
// fixed ".tmp" and no "*.tmp-*" residue in the directory.
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
			continue // cross-process lock file is expected, not residue
		}
		leftover = append(leftover, filepath.Base(e))
	}
	if len(leftover) > 0 {
		t.Fatalf("temp file residue after Save: %v", leftover)
	}
}
