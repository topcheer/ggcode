package session

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestTaskBoardMetaPersistence verifies the full round trip through the
// JSONL meta path: Session.TasksJSON -> meta record on disk -> Load.
// This is the wire format the TUI relies on to survive restarts and /resume.
func TestTaskBoardMetaPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "plan the release"}}}}
	// Opaque board snapshot as produced by task.Manager.SnapshotJSON.
	ses.TasksJSON = []byte(`{"version":1,"next_id":2,"tasks":[{"id":"task-1","subject":"ship","status":"pending"}]}`)

	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.TasksJSON) == 0 {
		t.Fatal("TasksJSON lost across Save/AppendMetaToDisk/Load")
	}
	if !strings.Contains(string(loaded.TasksJSON), `"task-1"`) {
		t.Errorf("board snapshot corrupted on disk: %s", loaded.TasksJSON)
	}

	// A later meta write with a new snapshot must win on reload.
	ses.TasksJSON = []byte(`{"version":1,"next_id":3,"tasks":[{"id":"task-2","subject":"second","status":"in_progress"}]}`)
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk (2nd): %v", err)
	}
	loaded2, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load (2nd): %v", err)
	}
	if !strings.Contains(string(loaded2.TasksJSON), `"task-2"`) {
		t.Errorf("latest board snapshot should win, got: %s", loaded2.TasksJSON)
	}
}

// TestTasksEnvFingerprintMetaPersistence verifies the workspace fingerprint
// captured alongside the board survives the same meta path (used by resume
// reconciliation to diff persisted claims against live environment drift).
func TestTasksEnvFingerprintMetaPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "fix the bug"}}}}
	ses.TasksJSON = []byte(`{"version":1,"next_id":1,"tasks":[{"id":"task-1","subject":"x","status":"in_progress"}]}`)
	ses.TasksEnvJSON = []byte(`{"captured_at":"2026-01-01T00:00:00Z","branch":"feat/x","head":"abc1234","dirty_files":["a.go"],"dirty_total":1}`)

	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(string(loaded.TasksEnvJSON), `"feat/x"`) {
		t.Errorf("environment fingerprint lost or corrupted on disk: %s", loaded.TasksEnvJSON)
	}

	// Latest meta write wins for the fingerprint too (board moved, workspace
	// re-snapshotted at a new HEAD).
	ses.TasksEnvJSON = []byte(`{"captured_at":"2026-01-02T00:00:00Z","branch":"main","head":"def5678"}`)
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk (2nd): %v", err)
	}
	loaded2, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load (2nd): %v", err)
	}
	if !strings.Contains(string(loaded2.TasksEnvJSON), `"main"`) {
		t.Errorf("latest fingerprint should win, got: %s", loaded2.TasksEnvJSON)
	}
}
