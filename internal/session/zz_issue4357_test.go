package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func issue4357Store(t *testing.T) (*JSONLStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"type":"message","role":"assistant","content":[{"type":"text","text":"orphan"}],"ts":"2026-10-22T10:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(dir, "orphan4357.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })
	return store, dir
}

func TestIssue4357OrphanRemoveFailureNotSwallowedNoInfiniteRescan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based unlink injection is not portable to Windows ACLs")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits; the unlink would succeed")
	}
	store, dir := issue4357Store(t)

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

func TestIssue4357InteractedOrphanKept(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })

	ses := &Session{ID: "keep4357", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ses.Messages = []provider.Message{{
		ID:      "keep4357-m1",
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "keep me"}},
	}}
	saveFullForTest(t, store, ses)

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
