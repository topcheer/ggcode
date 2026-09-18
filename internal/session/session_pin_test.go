package session

import (
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func pinTestSession(t *testing.T, store *JSONLStore) *Session {
	t.Helper()
	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	saveFullForTest(t, store, ses)
	return ses
}

func TestSetPinnedPersistsAcrossReload(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_pin_*")
	defer os.RemoveAll(dir)
	store, _ := NewJSONLStore(dir)
	ses := pinTestSession(t, store)

	if err := store.SetPinned(ses, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Pinned {
		t.Fatal("expected pinned session to reload as pinned")
	}

	if err := store.SetPinned(ses, false); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Pinned {
		t.Fatal("expected unpinned session to reload as unpinned")
	}
}

func TestSetTagsNormalizes(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_tags_*")
	defer os.RemoveAll(dir)
	store, _ := NewJSONLStore(dir)
	ses := pinTestSession(t, store)

	tags := []string{" Rust ", "rust", "", "PERF", "  "}
	if err := store.SetTags(ses, tags); err != nil {
		t.Fatal(err)
	}
	want := []string{"Rust", "PERF"}
	if len(ses.Tags) != len(want) {
		t.Fatalf("tags = %v, want %v", ses.Tags, want)
	}
	for i := range want {
		if ses.Tags[i] != want[i] {
			t.Fatalf("tags[%d] = %q, want %q", i, ses.Tags[i], want[i])
		}
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tags) != len(want) || loaded.Tags[0] != "Rust" {
		t.Fatalf("reloaded tags = %v, want %v", loaded.Tags, want)
	}

	// Clearing with an empty set persists the clear.
	if err := store.SetTags(ses, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tags) != 0 {
		t.Fatalf("expected tags cleared, got %v", loaded.Tags)
	}
}

func TestCleanupOlderThanSkipsPinned(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_cleanup_*")
	defer os.RemoveAll(dir)
	store, _ := NewJSONLStore(dir)

	old := pinTestSession(t, store)
	old.UpdatedAt = time.Now().Add(-48 * time.Hour)
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPinned(old, true); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	removed, err := store.CleanupOlderThan(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("pinned session should survive cleanup, removed=%d", removed)
	}
	if _, err := store.Load(old.ID); err != nil {
		t.Fatalf("pinned session missing after cleanup: %v", err)
	}
}

func TestListPinsFirst(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_listpin_*")
	defer os.RemoveAll(dir)
	store, _ := NewJSONLStore(dir)

	recent := pinTestSession(t, store)
	recent.Title = "recent"
	if err := store.Save(recent); err != nil {
		t.Fatal(err)
	}
	older := pinTestSession(t, store)
	older.Title = "older-but-pinned"
	older.UpdatedAt = time.Now().Add(-2 * time.Hour)
	if err := store.Save(older); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPinned(older, true); err != nil {
		t.Fatal(err)
	}

	sessions, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) < 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].ID != older.ID {
		t.Fatalf("pinned session should list first, got %q", sessions[0].Title)
	}
	if !sessions[0].Pinned {
		t.Fatal("List result missing Pinned flag")
	}
}
