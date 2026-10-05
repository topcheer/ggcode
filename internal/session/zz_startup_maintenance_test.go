package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// The lock-hold regression itself is guarded deterministically by
// TestRunMaintenanceDoesNotHoldStoreLock in zz_maintenance_fifo_test.go
// (FIFO block point; an earlier Load-vs-maintenance race test here missed
// the bug on small fixtures because maintenance finished before the probe).

// pruneInvalidIndexEntries must keep its three-state semantics after the
// switch from loadSessionFull to HasUserInteractionOnDisk (#291/#709):
// sessions with user text are kept, sessions without are pruned.
func TestPruneInvalidIndexEntriesStreamingSemantics(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	withUser := NewSession("test", "ep", "m")
	withUser.Workspace = dir
	withUser.Messages = []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "bot"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "real question"}}},
	}
	saveFullForTest(t, s, withUser)
	if err := s.updateIndex(withUser); err != nil {
		t.Fatalf("index withUser: %v", err)
	}

	noUser := NewSession("test", "ep", "m")
	noUser.Workspace = dir
	noUser.Messages = []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "only assistant"}}},
	}
	saveFullForTest(t, s, noUser)
	if err := s.updateIndex(noUser); err != nil {
		t.Fatalf("index noUser: %v", err)
	}

	idx, err := s.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	if len(idx) < 2 {
		t.Fatalf("expected >=2 index entries, got %d", len(idx))
	}
	valid, cleaned := s.pruneInvalidIndexEntries(idx)
	if !cleaned {
		t.Fatal("expected cleaned=true for the no-user session")
	}
	ids := map[string]bool{}
	for _, e := range valid {
		ids[e.ID] = true
	}
	if !ids[withUser.ID] {
		t.Fatal("session with user interaction was pruned")
	}
	if ids[noUser.ID] {
		t.Fatal("session without user interaction survived prune")
	}
}
