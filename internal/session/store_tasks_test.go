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
