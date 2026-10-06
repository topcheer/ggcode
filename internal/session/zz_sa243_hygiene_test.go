package session

// #3337 (sa-243 runtime audit): two store hygiene gaps pinned here.
//
// 1. Delete() must drop the .flock sidecar together with the .jsonl -
//    previously the sidecar leaked forever on every deleted session
//    (~35 orphans observed in a real profile).
// 2. CleanupOlderThan keeps Pinned sessions and removes only expired
//    ones; the wiring test pins the 90d default contract used by the
//    root.run() startup sweep.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestDeleteRemovesFlockSidecar(t *testing.T) {
	dir := sa146TempDir(t)
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("wire-test", "http://ep", "gpt-test")
	ses.Title = "del-flock"
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	path := store.sessionPath(ses.ID)
	sidecar := path + ".flock"
	if err := os.WriteFile(sidecar, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ses.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file must be gone, stat err=%v", err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatalf("flock sidecar must be removed with the session, stat err=%v", err)
	}
}

func TestDeleteToleratesMissingFlockSidecar(t *testing.T) {
	dir := sa146TempDir(t)
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("wire-test", "http://ep", "gpt-test")
	ses.Title = "del-noflock"
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ses.ID); err != nil {
		t.Fatalf("Delete without pre-existing sidecar must succeed: %v", err)
	}
}

func TestCleanupOlderThanKeepsPinnedAndFresh(t *testing.T) {
	// sa146TempDir (not t.TempDir): store async goroutines race cleanup.
	dir := sa146TempDir(t)
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := NewSession("wire-test", "http://ep", "gpt-test")
	old.Title = "old"
	old.UpdatedAt = time.Now().AddDate(0, 0, -120)
	oldPinned := NewSession("wire-test", "http://ep", "gpt-test")
	oldPinned.Title = "old-pinned"
	oldPinned.UpdatedAt = time.Now().AddDate(0, 0, -120)
	oldPinned.Pinned = true
	fresh := NewSession("wire-test", "http://ep", "gpt-test")
	fresh.Title = "fresh"
	userMsg := provider.Message{ID: "sa243-msg-1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}
	old.Messages = []provider.Message{userMsg}
	oldPinned.Messages = []provider.Message{func() provider.Message { m := userMsg; m.ID = "sa243-msg-2"; return m }()}
	fresh.Messages = []provider.Message{func() provider.Message { m := userMsg; m.ID = "sa243-msg-3"; return m }()}
	// Save() stamps UpdatedAt=now itself, so the age must be injected via
	// the metadata record afterwards (same discovery path as the pin tests).
	mkAge := func(s *Session, age time.Duration) {
		if err := store.Save(s); err != nil {
			t.Fatal(err)
		}
		s.UpdatedAt = time.Now().Add(-age)
		if err := store.AppendMetaToDisk(s); err != nil {
			t.Fatal(err)
		}
	}
	mkAge(old, 120*24*time.Hour)
	mkAge(oldPinned, 120*24*time.Hour)
	if err := store.SetPinned(oldPinned, true); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(fresh); err != nil {
		t.Fatal(err)
	}

	removed, err := store.CleanupOlderThan(time.Now().AddDate(0, 0, -90))
	if err != nil {
		t.Fatalf("CleanupOlderThan: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (only the unpinned expired one)", removed)
	}
	for id, want := range map[string]bool{oldPinned.ID: true, fresh.ID: true} {
		if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); err != nil && want {
			t.Fatalf("session %s must survive: %v", id, err)
		}
	}
}
