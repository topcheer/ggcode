package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestCleanupIfEmpty_DeletesEmptySession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	// No messages — no user interaction.
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file should exist after Save: %v", err)
	}

	// CleanupIfEmpty should remove it.
	if err := store.CleanupIfEmpty(ses); err != nil {
		t.Fatalf("CleanupIfEmpty: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty session file should be deleted by CleanupIfEmpty")
	}
}

func TestCleanupIfEmpty_KeepsNonEmptySession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	saveFullForTest(t, store, ses)
	path := filepath.Join(dir, ses.ID+".jsonl")

	// CleanupIfEmpty should be a no-op.
	if err := store.CleanupIfEmpty(ses); err != nil {
		t.Fatalf("CleanupIfEmpty: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("non-empty session file should still exist: %v", err)
	}

	// Verify the session can still be loaded.
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load after CleanupIfEmpty: %v", err)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(loaded.Messages))
	}
}

func TestCleanupIfEmpty_RemovesIndexEntry(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	// Add a user message so HasUserInteraction() is true — AppendMetaToDisk
	// requires user interaction to actually write.
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "temp"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatal(err)
	}

	// Verify the session is in the index.
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list {
		if e.ID == ses.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("session should be in index after AppendMetaToDisk")
	}

	// Now simulate the session losing its messages (e.g. in-memory state cleared
	// on exit). CleanupIfEmpty should remove both the file and index entry.
	ses.Messages = nil
	if err := store.CleanupIfEmpty(ses); err != nil {
		t.Fatalf("CleanupIfEmpty: %v", err)
	}

	list, err = store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range list {
		if e.ID == ses.ID {
			t.Errorf("session %s should not appear in list after cleanup", ses.ID)
		}
	}
}

func TestCleanupIfEmpty_FileAlreadyDeleted(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	// Don't create any file. CleanupIfEmpty should still succeed (Delete is
	// tolerant of missing files).
	if err := store.CleanupIfEmpty(ses); err != nil {
		t.Fatalf("CleanupIfEmpty on non-existent session: %v", err)
	}
}

func TestSave_OnlyCreatesFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	ses.Title = "My Title"

	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	// Save should create an empty file (no records written).
	path := filepath.Join(dir, ses.ID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("Save should create an empty file, got size %d", info.Size())
	}

	// The session should NOT appear in the index (no meta/messages appended).
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		if e.ID == ses.ID {
			t.Errorf("session should not appear in index after Save alone")
		}
	}
}

func TestSave_AppendAfterSave(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	// Add a user message so AppendMetaToDisk actually writes (it skips
	// sessions without user interaction).
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "seed"}}},
	}

	// Save just creates the file.
	store.Save(ses)

	// AppendMetaToDisk should work on the file created by Save.
	ses.Title = "After Save"
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}

	// AppendMessageToDisk should also work.
	msg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "appended"}}}
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatalf("AppendMessageToDisk: %v", err)
	}

	// Verify data can be loaded.
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "After Save" {
		t.Errorf("expected title 'After Save', got %q", loaded.Title)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("expected 1 message from disk, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content[0].Text != "appended" {
		t.Errorf("expected 'appended', got %q", loaded.Messages[0].Content[0].Text)
	}
}

func TestSave_UpdatesTimestamp(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	original := ses.UpdatedAt

	time.Sleep(10 * time.Millisecond)
	store.Save(ses)

	if !ses.UpdatedAt.After(original) {
		t.Errorf("Save should update UpdatedAt; got %v, want after %v", ses.UpdatedAt, original)
	}
}
