package session

// #4353 regression: List / LatestForWorkspace / ListForWorkspace must run
// the empty/stale-index repair (repairIndex full disk scan) OUTSIDE s.mu.
// The refactor moved the scan into repairIndexOutsideLock (unlock, scan
// under the index flock, relock, reload); these probes pin the functional
// invariant that all three paths still self-heal a wiped index and return
// the on-disk sessions - the repair just must not pin the store lock while
// doing it (the ~48s real-store scan cost that got runMaintenance
// de-locked in the first place).

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

const issue4353Body = `{"type":"message","role":"user","content":"hello","ts":"2026-10-22T10:00:00Z"}
{"type":"message","role":"assistant","content":"world","ts":"2026-10-22T10:00:01Z"}
`

func issue4353Store(t *testing.T) (*JSONLStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Two on-disk sessions in different workspaces, written through Save
	// (canonical schema), then the index is wiped below - simulating a
	// lost/stale index with real session files on disk.
	for _, id := range []string{"a4353", "b4353"} {
		ws := "/ws/one"
		if id == "b4353" {
			ws = "/ws/two"
		}
		ses := &Session{ID: id, Title: "t-" + id, Workspace: ws, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := store.Save(ses); err != nil {
			t.Fatal(err)
		}
		// Repair only re-indexes sessions with user interaction on disk.
		if err := store.AppendMessage(ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi " + id}}}); err != nil {
			t.Fatal(err)
		}
	}
	// Wipe the index: len(idx)==0 / missing-workspace repair trigger.
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Let the store's async maintenance goroutine quiesce BEFORE t.TempDir
	// removal (cleanup runs LIFO: this fires first, avoiding the
	// "directory not empty" unlinkat race observed without it).
	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })
	return store, dir
}

func TestIssue4353ListRepairsOutsideLock(t *testing.T) {
	store, _ := issue4353Store(t)
	sessions, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("List after wiped index = %d sessions, want 2 (repair must still self-heal)", len(sessions))
	}
}

func TestIssue4353WorkspacePathsRepairOutsideLock(t *testing.T) {
	store, _ := issue4353Store(t)
	// Sessions land in the cwd workspace (loadSession's fallback when no
	// explicit workspace record exists) - the repair trigger (wiped index
	// missing this workspace) and the re-index path are identical.
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	one, err := store.ListForWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 2 {
		t.Fatalf("ListForWorkspace(cwd) = %d, want 2", len(one))
	}

	latest, err := store.LatestForWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("LatestForWorkspace(cwd) = nil, want the repaired session")
	}
}

// TestIssue4353StoreLockFreeDuringRepair is the liveness probe: while a
// repair-triggering List runs, a Save on the same store must be able to
// acquire s.mu promptly. With a two-file store the repair is fast, so the
// assertion is a smoke (no deadlock/lock-leak) rather than a timing
// proof; the structural guarantee is the unlock/relock pairing inside
// repairIndexOutsideLock.
func TestIssue4353StoreLockFreeDuringRepair(t *testing.T) {
	store, _ := issue4353Store(t)

	done := make(chan struct{})
	go func() {
		_, _ = store.List()
		close(done)
	}()

	saveDone := make(chan struct{})
	go func() {
		_ = store.Save(&Session{ID: "c4353", Title: "probe", UpdatedAt: time.Now(), CreatedAt: time.Now()})
		close(saveDone)
	}()

	select {
	case <-saveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("#4353 regression: store lock unavailable during repair-triggering List")
	}
	<-done
}
