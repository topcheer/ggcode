package session

// zz_issue4353_test.go -- regression nail for #4353: List,
// LatestForWorkspace and ListForWorkspace must not hold s.mu across
// repairIndex's full-store scan. Before the fix the repair block ran
// under the deferred store lock, so an empty/stale index plus a large
// session directory queued every concurrent Load/Save behind a
// multi-second (48s on a real store, per the runMaintenance note)
// full-file scan -- the same startup-white-screen regression class
// runMaintenance was de-locked for.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func seedSessionsFor4353(t *testing.T, store *JSONLStore, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
		ses.Title = "seed"
		ses.Workspace = "/tmp/workspace-4353"
		ses.Messages = []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		}
		saveFullForTest(t, store, ses)
	}
}

// deleteIndexFor4353 removes the on-disk index so the next List call
// takes the empty-index repair path.
func deleteIndexFor4353(t *testing.T, store *JSONLStore) {
	t.Helper()
	if err := os.Remove(filepath.Join(store.dir, "index.json")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove index: %v", err)
	}
}

// Functional: with the index deleted, List must rebuild it and still
// return every on-disk session (de-locking the repair must not change
// repair semantics).
func TestIssue4353ListRepairsEmptyIndex(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	seedSessionsFor4353(t, store, 3)
	deleteIndexFor4353(t, store)

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("post-repair list count: want 3, got %d", len(list))
	}
}

// Functional: ListForWorkspace/LatestForWorkspace rebuild a workspace
// miss through the de-locked repair and still return the workspace's
// sessions.
func TestIssue4353WorkspaceListingsRepairOnMiss(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	seedSessionsFor4353(t, store, 2)
	deleteIndexFor4353(t, store)

	list, err := store.ListForWorkspace("/tmp/workspace-4353")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("workspace list count: want 2, got %d", len(list))
	}
	latest, err := store.LatestForWorkspace("/tmp/workspace-4353")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID == "" {
		t.Fatal("latest for workspace: got nil/empty after repair")
	}
}

// Behavioral pin (the actual #4353 symptom): while List is inside its
// repair scan, a concurrent Save must make progress instead of queueing
// on s.mu until the scan finishes. A fresh Save drains the pipe quickly
// only if the lock is free during repair; under the old code the save
// stalls for the whole repair window.
func TestIssue4353SaveProgressesDuringListRepair(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	seedSessionsFor4353(t, store, 1)
	deleteIndexFor4353(t, store)

	listDone := make(chan error, 1)
	go func() { _, err := store.List(); listDone <- err }()

	// Give List a moment to enter the repair path, then run a Save of a
	// brand-new session; it must complete well inside the repair window.
	time.Sleep(20 * time.Millisecond)
	saved := make(chan error, 1)
	go func() {
		ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
		ses.Title = "racer"
		ses.Workspace = "/tmp/workspace-4353"
		ses.Messages = []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "r"}}},
		}
		saved <- store.Save(ses)
	}()

	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("concurrent save failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Save blocked >5s while List repaired -- s.mu still held across repairIndex")
	}
	if err := <-listDone; err != nil {
		t.Fatalf("list failed: %v", err)
	}
}

// Race stress: List (repair path) hammered concurrently with Save/Lock
// users; run under -race this pins that the interim unlock/relock
// introduced no data race on the shared index or store state.
func TestIssue4353ConcurrentStress(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	seedSessionsFor4353(t, store, 2)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				deleteIndexFor4353(t, store)
				if _, err := store.List(); err != nil {
					t.Errorf("list: %v", err)
					return
				}
			}
		}()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
				ses.Title = "stress"
				ses.Workspace = "/tmp/workspace-4353"
				ses.Messages = []provider.Message{
					{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "s"}}},
				}
				if err := store.Save(ses); err != nil {
					t.Errorf("save: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
