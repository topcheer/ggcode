package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4822_MetaOnlySessionSurvivesPrune(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{
		ID:        "20260401-000000",
		Title:     "Session 0",
		Workspace: "/ws",
		Vendor:    "test",
		Model:     "m1",
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg"}}},
		},
	}
	ses.CreatedAt = time.Now().Add(-time.Hour)
	ses.UpdatedAt = time.Now().Add(-time.Hour)
	if err := s.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMetaToDisk(ses); err != nil {
		t.Fatal(err)
	}

	idx := []indexEntry{sessionToIndexEntry(ses)}
	s.mu.Lock()
	out, cleaned := s.pruneInvalidIndexEntries(idx)
	s.mu.Unlock()
	if cleaned {
		t.Fatal("prune cleaned a meta-only session (creation window)")
	}
	if len(out) != 1 || out[0].ID != ses.ID {
		t.Fatalf("prune dropped the meta-only entry: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ses.ID+".jsonl")); err != nil {
		t.Fatalf("meta-only session file was deleted by prune: %v", err)
	}

	s.mu.Lock()
	repaired, rerr := s.repairIndex(nil)
	s.mu.Unlock()
	if rerr != nil {
		t.Fatal(rerr)
	}
	_ = repaired
	if _, err := os.Stat(filepath.Join(dir, ses.ID+".jsonl")); err != nil {
		t.Fatalf("meta-only session file was deleted by repairIndex: %v", err)
	}
}

func TestIssue4822_BareTouchFileStillPruned(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "20260401-000001", Title: "x", Workspace: "/ws"}
	ses.CreatedAt = time.Now()
	ses.UpdatedAt = time.Now()
	if err := s.Save(ses); err != nil {
		t.Fatal(err)
	}
	idx := []indexEntry{sessionToIndexEntry(ses)}
	s.mu.Lock()
	_, cleaned := s.pruneInvalidIndexEntries(idx)
	s.mu.Unlock()
	if cleaned {
		t.Fatal("bare touch-only session inside the creation window must be kept")
	}

	s.mu.Lock()
	s.createdPending[ses.ID] = time.Now().Add(-2 * sessionCreationWindow)
	_, cleaned = s.pruneInvalidIndexEntries(idx)
	s.mu.Unlock()
	if !cleaned {
		t.Fatal("expired bare touch-only session should be pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, ses.ID+".jsonl")); !os.IsNotExist(err) {
		t.Fatal("expired bare touch-only session file should have been removed")
	}
}
