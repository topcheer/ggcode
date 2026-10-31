package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4822: a freshly created session transits Save(touch) -> AppendMetaToDisk
// -> first user-message batch. In that window the file has a meta record but
// NO user interaction on disk; pruneInvalidIndexEntries and repairIndex used
// to delete such files outright (#4668's flock probe only guards cross-process
// writers), so the batch later recreated a meta-less file and the title was
// lost ("expected Session 0, got empty"). Meta-bearing files must be kept by
// both prune paths; bare touch-only files stay prunable.
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
	// Creation window: meta on disk, user message batch NOT yet appended.

	// pruneInvalidIndexEntries must keep the meta-only session.
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

	// repairIndex's orphan scan must not delete it either.
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

// The original cleanup goal stays intact: a bare touch-only file is protected
// only inside the creation window; once the window expires (or after a
// process restart, where the registry is empty), it is pruned again.
func TestIssue4822_BareTouchFileStillPruned(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := &Session{ID: "20260401-000001", Title: "x", Workspace: "/ws"}
	ses.CreatedAt = time.Now()
	ses.UpdatedAt = time.Now()
	if err := s.Save(ses); err != nil { // touch only, registers the window
		t.Fatal(err)
	}
	idx := []indexEntry{sessionToIndexEntry(ses)}
	s.mu.Lock()
	_, cleaned := s.pruneInvalidIndexEntries(idx)
	s.mu.Unlock()
	if cleaned {
		t.Fatal("bare touch-only session inside the creation window must be kept")
	}

	// Age the registry entry past the window: the prune must now remove it.
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
