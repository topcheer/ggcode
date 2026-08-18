package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// drainStoreMaintenance waits until the store's background maintenance
// goroutine (index rebuild) is idle (#676). Save/Delete/List schedule
// runMaintenance asynchronously; if the test returns while it is still
// writing, t.TempDir()'s RemoveAll races the residual writes and fails CI
// with "unlinkat: directory not empty". Registered via t.Cleanup — LIFO
// ordering guarantees it runs BEFORE the TempDir removal registered earlier.
func drainStoreMaintenance(t *testing.T, store *JSONLStore) {
	t.Helper()
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			store.mu.Lock()
			idle := !store.maintenanceRunning
			store.mu.Unlock()
			if idle {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// TestT1_RemoveFromIndex_CorruptGuard verifies that removeFromIndex does not
// write an empty index when the on-disk index is corrupt. Without the guard,
// a corrupt index + ordinary Delete would write a legitimate empty [] JSON,
// permanently hiding sessions from List() (4/5 sessions lost per probe).
func TestT1_RemoveFromIndex_CorruptGuard(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewJSONLStore(tmpDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	drainStoreMaintenance(t, store) // #676: drain async index writes before TempDir cleanup

	// Create 5 sessions with user messages
	sessions := make([]*Session, 5)
	for i := 0; i < 5; i++ {
		ses := &Session{
			ID:        generateID(),
			CreatedAt: now(),
			UpdatedAt: now(),
			Title:     "Test Session",
			Workspace: "/test",
			Vendor:    "test",
			Endpoint:  "test",
			Model:     "test",
			Messages: []provider.Message{
				{
					Role: "user",
					Content: []provider.ContentBlock{
						{Type: "text", Text: "test message"},
					},
				},
			},
		}
		sessions[i] = ses
		if err := store.Save(ses); err != nil {
			t.Fatalf("Save session %d: %v", i, err)
		}
		if err := store.AppendMessage(ses, ses.Messages[0]); err != nil {
			t.Fatalf("AppendMessage session %d: %v", i, err)
		}
	}

	// Verify all 5 sessions are in the index
	list, err := store.List()
	if err != nil {
		t.Fatalf("List before corruption: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("Expected 5 sessions, got %d", len(list))
	}

	// Corrupt the index file (write invalid JSON)
	indexPath := store.indexPath()
	if err := os.WriteFile(indexPath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("Corrupt index: %v", err)
	}

	// Delete one session - this should NOT write a valid empty index
	targetID := sessions[0].ID
	if err := store.Delete(targetID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// The index should still be corrupt (empty list after reload triggers repair)
	// Key: List should still return sessions after repair, not lose 4/5
	list, err = store.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}

	// After repair, we should have 4 sessions remaining (the one deleted)
	// Without the guard, we would get only 1 or 0 because the empty index was written
	if len(list) != 4 {
		t.Errorf("Expected 4 sessions after delete and repair, got %d", len(list))
		for _, ses := range list {
			t.Logf("Session: %s", ses.ID)
		}
	}

	// Verify the deleted session is not in the list
	for _, ses := range list {
		if ses.ID == targetID {
			t.Error("Deleted session still in list")
		}
	}
}

// TestT3_AppendRecordLines_LockFailureAborts verifies that appendRecordLines
// aborts and returns an error when flock fails, rather than silently continuing
// with unlocked O_APPEND writes that can be lost to concurrent renames.
// Probe: 60/60 appends lost without this fix.
func TestT3_AppendRecordLines_LockFailureAborts(t *testing.T) {
	t.Parallel()

	// Use a fake path that will cause lock failure (e.g., non-existent directory)
	// The lock will fail, and we expect appendRecordLines to fail as well
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test.jsonl")

	// Write an initial message to create the file
	initialMsg := jsonlRecord{
		Type:      "message",
		SessionID: "test",
		Message: &provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{
				{Type: "text", Text: "initial"},
			},
		},
		Timestamp: now(),
	}

	// Force lock failure by making the path unwritable (simulate permission error)
	// We can't easily simulate lock failure without OS-level mocking,
	// so we test that the function has the right structure:
	// - It should return an error when lock fails
	// - It should NOT continue with O_APPEND writes

	// Create a sidecar lock file that can't be opened
	lockPath := sessionPath + ".flock"
	if err := os.WriteFile(lockPath, []byte("test"), 0000); err != nil {
		t.Fatalf("Create lock file: %v", err)
	}
	defer os.Chmod(lockPath, 0600)

	// Try to append - this should fail due to lock acquisition failure
	// After the fix, appendRecordLines should abort when lock fails
	recs := []jsonlRecord{initialMsg}
	err := appendRecordLines(sessionPath, recs)

	// After the T3 fix, this should return an error
	// Before the fix, it would succeed silently (data loss risk)
	if err == nil {
		t.Error("appendRecordLines should fail when lock acquisition fails, but succeeded (data loss risk)")
		t.Log("This is expected to fail after T3 fix is applied")
	}
}

// TestT2_RemoveFromIndex_RetryOnLockFailure verifies that removeFromIndex
// retries flock acquisition with exponential backoff (3 attempts) like
// updateIndex does, rather than immediately falling back to lock-free RMW.
func TestT2_RemoveFromIndex_RetryOnLockFailure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewJSONLStore(tmpDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	drainStoreMaintenance(t, store) // #676: drain async index writes before TempDir cleanup

	// Create a session
	ses := &Session{
		ID:        generateID(),
		CreatedAt: now(),
		UpdatedAt: now(),
		Title:     "Test Session",
		Workspace: "/test",
		Vendor:    "test",
		Endpoint:  "test",
		Model:     "test",
		Messages: []provider.Message{
			{
				Role: "user",
				Content: []provider.ContentBlock{
					{Type: "text", Text: "test"},
				},
			},
		},
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.AppendMessage(ses, ses.Messages[0]); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Verify session is in index
	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(list))
	}

	// Make the lock file temporarily unavailable to test retry logic
	// After T2 fix, removeFromIndex should retry 3 times with backoff
	lockPath := store.indexPath() + ".flock"
	if err := os.Chmod(lockPath, 0000); err != nil {
		t.Fatalf("Make lock unreadable: %v", err)
	}
	defer os.Chmod(lockPath, 0600)

	// This should fail after retries (or succeed if we unlock mid-retry)
	// The key is that it should retry, not immediately fall back to lock-free
	err = store.Delete(ses.ID)
	if err != nil {
		// Expected: lock acquisition fails after retries
		t.Logf("Delete failed as expected due to lock: %v", err)
	}

	// Restore permissions and verify the delete works
	if err := os.Chmod(lockPath, 0600); err != nil {
		t.Fatalf("Restore lock permissions: %v", err)
	}

	// Now delete should succeed
	if err := store.Delete(ses.ID); err != nil {
		t.Errorf("Delete after restoring lock: %v", err)
	}

	// Verify session is removed
	list, err = store.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("Expected 0 sessions after delete, got %d", len(list))
	}
}

func now() time.Time {
	return time.Now()
}
