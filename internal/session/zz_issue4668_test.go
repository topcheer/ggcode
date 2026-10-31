package session

import (
	"os"
	"testing"
)

// #4668 companion tests: session store maintenance must respect the
// per-session flock and other processes' deletions.
//   A. prune/repair must NOT unlink a session whose flock is held by a
//      concurrent writer (pre-first-user-text window: append lands in an
//      orphaned fd, data silently lost).
//   B. the maintenance index merge must NOT resurrect an entry another
//      process Deleted while we scanned (ghost rows in List that cannot
//      be opened).

func newIssue4668Store(t *testing.T) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

// newIssue4668EmptySession persists a session with NO user text - the prune
// target (HasUserInteractionOnDisk == false, file exists). NewSession/Save
// seeds the index row; the file is then rewritten to a single tool_result-only
// user record (role=user, content has no text block) - exactly the
// pre-first-user-text window from the issue.
func newIssue4668EmptySession(t *testing.T, store *JSONLStore, id string) *Session {
	t.Helper()
	ses := NewSession("zai", "default", "glm-5")
	ses.ID = id
	if err := store.Save(ses); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
	toolResultOnly := `{"type":"message","message":{"id":"m1","role":"user","content":[{"type":"tool_result","tool_use_id":"t1"}]}}` + "\n"
	if err := os.WriteFile(store.sessionPath(id), []byte(toolResultOnly), 0o644); err != nil {
		t.Fatalf("rewrite %s: %v", id, err)
	}
	interacted, err := store.HasUserInteractionOnDisk(id)
	if err != nil || interacted {
		t.Fatalf("fixture %s: interacted=%v err=%v, want empty non-interacted session", id, interacted, err)
	}
	return ses
}

func TestIssue4668PruneKeepsFlockHeldSession(t *testing.T) {
	store := newIssue4668Store(t)
	ses := newIssue4668EmptySession(t, store, "held-4668")

	// Simulate a cross-process writer holding the per-session flock. On macOS
	// a same-process second flock CONVERTS the lock (flock(2)) instead of
	// conflicting, so the real SessionLock cannot exercise the guard from
	// inside the test process; the probe var stands in for the other process.
	orig := sessionLockProbe
	sessionLockProbe = func(storeDir, sessionID string) bool {
		return sessionID == "held-4668"
	}
	t.Cleanup(func() { sessionLockProbe = orig })

	idx := []indexEntry{sessionToIndexEntry(ses)}
	valid, cleaned := store.pruneInvalidIndexEntries(idx)

	// The flock-held session must survive BOTH the file and the index row.
	if _, statErr := os.Stat(store.sessionPath("held-4668")); statErr != nil {
		t.Fatalf("flock-held session unlinked during prune: %v", statErr)
	}
	found := false
	for _, e := range valid {
		if e.ID == "held-4668" {
			found = true
		}
	}
	if !found {
		t.Fatal("flock-held session evicted from index during prune")
	}
	if cleaned {
		t.Fatal("cleaned=true; a kept (locked) session must not count as pruned")
	}
}

func TestIssue4668RepairKeepsFlockHeldOrphan(t *testing.T) {
	store := newIssue4668Store(t)
	newIssue4668EmptySession(t, store, "orphan-held-4668")

	// Drop it from the index so repairIndex sees it as a disk orphan.
	idx, _ := store.loadIndexNoRepair()
	filtered := make([]indexEntry, 0, len(idx))
	for _, e := range idx {
		if e.ID != "orphan-held-4668" {
			filtered = append(filtered, e)
		}
	}
	if err := store.saveIndex(filtered); err != nil {
		t.Fatalf("rewrite index: %v", err)
	}

	// Same cross-process simulation as the prune test (macOS flock converts
	// same-process locks instead of conflicting).
	orig := sessionLockProbe
	sessionLockProbe = func(storeDir, sessionID string) bool {
		return sessionID == "orphan-held-4668"
	}
	t.Cleanup(func() { sessionLockProbe = orig })

	if _, err := store.RepairIndex(); err != nil {
		t.Fatalf("repairIndex: %v", err)
	}
	if _, statErr := os.Stat(store.sessionPath("orphan-held-4668")); statErr != nil {
		t.Fatalf("flock-held orphan unlinked during repair: %v", statErr)
	}
}

func TestIssue4668MergeDoesNotResurrectDeletedEntry(t *testing.T) {
	store := newIssue4668Store(t)
	ses := newIssue4668EmptySession(t, store, "ghost-4668")

	// Pre-scan snapshot: entry present and valid (a session that HAD user
	// interaction when scanned - simulated by the untouched index row).
	idx := []indexEntry{sessionToIndexEntry(ses)}
	validIdx := idx // survived the prune

	// Another process Deletes while we scanned: row gone AND file gone. The
	// locked reload (currentIdx) reflects the delete.
	if err := store.Delete("ghost-4668"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	currentIdx := []indexEntry{}

	merged := store.mergeMaintainedIndex(idx, validIdx, currentIdx)
	for _, e := range merged {
		if e.ID == "ghost-4668" {
			t.Fatal("deleted entry resurrected into merged index (ghost List row)")
		}
	}
}

func TestIssue4668MergeKeepsValidEntryMissingFromReload(t *testing.T) {
	// The opposite direction: the entry's file still exists on disk but the
	// locked reload lost the row (e.g. another process rewrote the index for
	// its own reasons). The merge must keep it - do not over-drop.
	store := newIssue4668Store(t)
	ses := newIssue4668EmptySession(t, store, "alive-4668")

	idx := []indexEntry{sessionToIndexEntry(ses)}
	validIdx := idx
	currentIdx := []indexEntry{} // row lost from reload, file still on disk

	merged := store.mergeMaintainedIndex(idx, validIdx, currentIdx)
	found := false
	for _, e := range merged {
		if e.ID == "alive-4668" {
			found = true
		}
	}
	if !found {
		t.Fatal("valid on-disk entry dropped from merged index (over-correction)")
	}
}
