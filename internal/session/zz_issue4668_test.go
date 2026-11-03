package session

import (
	"os"
	"testing"
)

func newIssue4668Store(t *testing.T) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

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

	orig := sessionLockProbe
	sessionLockProbe = func(storeDir, sessionID string) bool {
		return sessionID == "held-4668"
	}
	t.Cleanup(func() { sessionLockProbe = orig })

	idx := []indexEntry{sessionToIndexEntry(ses)}
	valid, cleaned := store.pruneInvalidIndexEntries(idx)

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

	idx := []indexEntry{sessionToIndexEntry(ses)}
	validIdx := idx

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

	store := newIssue4668Store(t)
	ses := newIssue4668EmptySession(t, store, "alive-4668")

	idx := []indexEntry{sessionToIndexEntry(ses)}
	validIdx := idx
	currentIdx := []indexEntry{}

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
