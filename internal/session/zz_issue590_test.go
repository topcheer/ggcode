package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

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

func TestT1_RemoveFromIndex_CorruptGuard(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewJSONLStore(tmpDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	drainStoreMaintenance(t, store)

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

	list, err := store.List()
	if err != nil {
		t.Fatalf("List before corruption: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("Expected 5 sessions, got %d", len(list))
	}

	indexPath := store.indexPath()
	if err := os.WriteFile(indexPath, []byte("{invalid json"), 0600); err != nil {
		t.Fatalf("Corrupt index: %v", err)
	}

	targetID := sessions[0].ID
	if err := store.Delete(targetID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	list, err = store.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}

	if len(list) != 4 {
		t.Errorf("Expected 4 sessions after delete and repair, got %d", len(list))
		for _, ses := range list {
			t.Logf("Session: %s", ses.ID)
		}
	}

	for _, ses := range list {
		if ses.ID == targetID {
			t.Error("Deleted session still in list")
		}
	}
}

func TestT3_AppendRecordLines_LockFailureAborts(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "test.jsonl")

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

	lockPath := sessionPath + ".flock"
	if err := os.WriteFile(lockPath, []byte("test"), 0000); err != nil {
		t.Fatalf("Create lock file: %v", err)
	}
	defer os.Chmod(lockPath, 0600)

	recs := []jsonlRecord{initialMsg}
	err := appendRecordLines(sessionPath, recs)

	if err == nil {
		t.Error("appendRecordLines should fail when lock acquisition fails, but succeeded (data loss risk)")
		t.Log("This is expected to fail after T3 fix is applied")
	}
}

func TestT2_RemoveFromIndex_RetryOnLockFailure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store, err := NewJSONLStore(tmpDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	drainStoreMaintenance(t, store)

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

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(list))
	}

	lockPath := store.indexPath() + ".flock"
	if err := os.Chmod(lockPath, 0000); err != nil {
		t.Fatalf("Make lock unreadable: %v", err)
	}
	defer os.Chmod(lockPath, 0600)

	err = store.Delete(ses.ID)
	if err != nil {

		t.Logf("Delete failed as expected due to lock: %v", err)
	}

	if err := os.Chmod(lockPath, 0600); err != nil {
		t.Fatalf("Restore lock permissions: %v", err)
	}

	if err := store.Delete(ses.ID); err != nil {
		t.Errorf("Delete after restoring lock: %v", err)
	}

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
