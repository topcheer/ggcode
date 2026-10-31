package session

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// waitForMaintenance quiesces the store's background maintenance goroutine
// (scheduled by List via scheduleMaintenanceLocked) so t.TempDir cleanup does
// not race with late index writes ("directory not empty" unlinkat failures).
func waitForMaintenance(t *testing.T, stores ...*JSONLStore) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		for _, s := range stores {
			if s == nil {
				continue
			}
			s.mu.Lock()
			running := s.maintenanceRunning
			s.mu.Unlock()
			if running || s.getIndexDirty() {
				busy = true
			}
		}
		if !busy {
			// One final grace tick: goroutine exit path may write after the
			// flags clear.
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// branch_lineage_test.go - branch lineage (ParentSessionID/ForkPoint)
// persistence and ListChildren, which back the /branches and /branch-switch
// TUI commands. Before this fix lineage was a write-only in-memory field:
// it never reached the JSONL meta record or the index, so a fork's parent
// was lost on reload and branch trees were undiscoverable.

func TestBranchLineagePersistedAndListed(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	userMsg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}

	parent := NewSession("vendor", "endpoint", "model")
	parent.Workspace = "/tmp/ws-branch"
	parent.Messages = []provider.Message{userMsg}
	saveFullForTest(t, store, parent)

	child := NewSession("vendor", "endpoint", "model")
	child.Workspace = "/tmp/ws-branch"
	child.Messages = []provider.Message{userMsg}
	child.ParentSessionID = parent.ID
	child.ForkPoint = 2
	saveFullForTest(t, store, child)

	// An unrelated session must not show up as a child.
	other := NewSession("vendor", "endpoint", "model")
	other.Workspace = "/tmp/ws-branch"
	other.Messages = []provider.Message{userMsg}
	saveFullForTest(t, store, other)

	// Simulate a process restart: a fresh store instance must see the
	// lineage recorded on disk.
	store2, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store2.Load(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ParentSessionID != parent.ID {
		t.Fatalf("after reload: ParentSessionID = %q, want %q", loaded.ParentSessionID, parent.ID)
	}
	if loaded.ForkPoint != 2 {
		t.Fatalf("after reload: ForkPoint = %d, want 2", loaded.ForkPoint)
	}

	// ListChildren works off the index and returns only real children,
	// with lineage fields populated.
	kids, err := store2.ListChildren(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("ListChildren(parent) returned %d sessions, want 1", len(kids))
	}
	if kids[0].ID != child.ID {
		t.Fatalf("ListChildren(parent)[0].ID = %q, want %q", kids[0].ID, child.ID)
	}
	if kids[0].ParentSessionID != parent.ID || kids[0].ForkPoint != 2 {
		t.Fatalf("ListChildren entry lineage = (%q, %d), want (%q, 2)", kids[0].ParentSessionID, kids[0].ForkPoint, parent.ID)
	}

	none, err := store2.ListChildren(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("ListChildren(unrelated) returned %d sessions, want 0", len(none))
	}
	waitForMaintenance(t, store, store2)
}

func TestBranchLineageSurvivesLaterMetaAppends(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	userMsg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}
	parent := NewSession("vendor", "endpoint", "model")
	parent.Workspace = "/tmp/ws-branch2"
	parent.Messages = []provider.Message{userMsg}
	saveFullForTest(t, store, parent)

	child := NewSession("vendor", "endpoint", "model")
	child.Workspace = "/tmp/ws-branch2"
	child.Messages = []provider.Message{userMsg}
	child.ParentSessionID = parent.ID
	child.ForkPoint = 1
	saveFullForTest(t, store, child)

	// Later meta appends (title changes, pin toggles, board snapshots...)
	// must not clobber the immutable lineage.
	child.Title = "Branch: renamed"
	if err := store.AppendMetaToDisk(child); err != nil {
		t.Fatal(err)
	}

	store2, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store2.Load(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ParentSessionID != parent.ID || loaded.ForkPoint != 1 {
		t.Fatalf("lineage after later meta append = (%q, %d), want (%q, 1)", loaded.ParentSessionID, loaded.ForkPoint, parent.ID)
	}
	if loaded.Title != "Branch: renamed" {
		t.Fatalf("title = %q, want %q", loaded.Title, "Branch: renamed")
	}
	waitForMaintenance(t, store, store2)
}
