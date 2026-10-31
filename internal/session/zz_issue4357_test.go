package session

// #4357 regression: repairIndex's orphan-file deletion swallowed the
// os.Remove error (`_ = os.Remove(...)`) and unconditionally set
// changed=true. When the unlink fails (permission, read-only mount,
// Windows file-in-use) the orphan never disappears, so every repair
// round re-detects it as changed - an infinite rescan/index-rewrite
// loop that also violates the #1307 error-discipline (no silently
// ignored errors). The fix follows pruneInvalidIndexEntries: on a real
// Remove failure, log it and leave the orphan un-indexed for the next
// pass WITHOUT flipping changed; IsNotExist counts as success.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// issue4357Store builds a store with exactly one non-interacted orphan
// session file on disk and NO index entry for it (repair trigger).
func issue4357Store(t *testing.T) (*JSONLStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Orphan file with only an assistant message: loadSession succeeds,
	// HasUserInteractionOnDisk is false -> deletion-eligible branch.
	body := `{"type":"message","role":"assistant","content":[{"type":"text","text":"orphan"}],"ts":"2026-10-22T10:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(dir, "orphan4357.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Quiesce the async maintenance goroutine before TempDir cleanup
	// (cleanup runs LIFO; mirrors zz_issue4353_test.go).
	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })
	return store, dir
}

// TestIssue4357OrphanRemoveFailureNotSwallowedNoInfiniteRescan pins the
// core fix: when the orphan's unlink fails, repairIndex must (a) still
// complete without error (deletion failure is not fatal), (b) NOT
// report the round as changed-by-the-orphan (return changed=false when
// nothing else changed), and (c) never re-add the stuck file to the
// index. Before the fix, changed was always true for a stuck orphan,
// driving the rescan loop.
func TestIssue4357OrphanRemoveFailureNotSwallowedNoInfiniteRescan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unlink injection is not portable to Windows ACLs")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits; the unlink would succeed")
	}
	store, dir := issue4357Store(t)

	// Make the orphan's unlink fail by stripping write permission from the
	// sessions directory (unlink permission lives on the directory, not the
	// file). Pre-create the index flock file while the dir is still
	// writable: lockIndexFile opens it O_CREATE, which needs dir write.
	flockPath := store.indexPath() + ".flock"
	if err := os.WriteFile(flockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	idx, err := store.loadIndexFromDisk()
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.repairIndex(idx)
	if err != nil {
		t.Fatalf("repairIndex must not fail on orphan remove failure: %v", err)
	}
	if changed {
		t.Fatal("repairIndex reported changed=true with only a stuck orphan - remove error was swallowed and the rescan loop (#4357) is back")
	}
	// The stuck orphan must stay on disk, un-indexed, for the next pass.
	if _, err := os.Stat(filepath.Join(dir, "orphan4357.jsonl")); err != nil {
		t.Fatalf("stuck orphan must remain on disk for the next repair pass: %v", err)
	}
	idx2, err := store.loadIndexFromDisk()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range idx2 {
		if e.ID == "orphan4357" {
			t.Fatal("stuck orphan was indexed; it must stay un-indexed for the next repair pass")
		}
	}
}

// TestIssue4357OrphanRemoveSuccessStillRepairs pins that the non-failure
// path is unchanged: a deletable orphan is removed and the round reports
// changed=true (dirty flag cleared, disk/index consistent again).
func TestIssue4357OrphanRemoveSuccessStillRepairs(t *testing.T) {
	store, dir := issue4357Store(t)

	idx, err := store.loadIndexFromDisk()
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.repairIndex(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("successful orphan removal must still report changed=true (repair consumed the orphan)")
	}
	if _, err := os.Stat(filepath.Join(dir, "orphan4357.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("deletable orphan still on disk: %v", err)
	}
}

// TestIssue4357InteractedOrphanKept pins that the failure handling did
// not disturb the keep-branch: an orphan WITH user interaction is kept
// and indexed, not deleted. The file is written through the store's own
// save path (canonical record format) and then orphan-ified by resetting
// the index to empty.
func TestIssue4357InteractedOrphanKept(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Quiesce the async maintenance goroutine before TempDir cleanup.
	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })

	ses := &Session{ID: "keep4357", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{{
		ID:      "keep4357-m1",
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "keep me"}},
	}}
	saveFullForTest(t, store, ses)
	// Orphan-ify: the file is on disk but the index forgets it.
	if err := os.WriteFile(store.indexPath(), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := store.loadIndexFromDisk()
	if err != nil {
		t.Fatal(err)
	}
	changed, err := store.repairIndex(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("indexing a kept orphan must report changed=true")
	}
	// repairIndex writes the rebuilt index to disk; it does not mutate the
	// caller's slice - re-load and scan the persisted index.
	idx2, err := store.loadIndexFromDisk()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range idx2 {
		if e.ID == "keep4357" {
			found = true
		}
	}
	if !found {
		t.Fatal("interacted orphan was not indexed")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep4357.jsonl")); err != nil {
		t.Fatalf("interacted orphan must not be deleted: %v", err)
	}
}
