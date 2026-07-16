package agentruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestSaveSessionMessages_IncrementalWrite(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)
	ses := session.NewSession("zai", "default", "model")

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello world"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi there"}}},
	}

	if err := SaveSessionMessages(store, ses, msgs); err != nil {
		t.Fatalf("SaveSessionMessages: %v", err)
	}

	// Verify the file exists and contains data.
	path := filepath.Join(dir, ses.ID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("file should not be empty after SaveSessionMessages with messages")
	}

	// Verify by loading from disk.
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content[0].Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", loaded.Messages[0].Content[0].Text)
	}
	if loaded.Messages[1].Content[0].Text != "hi there" {
		t.Errorf("expected 'hi there', got %q", loaded.Messages[1].Content[0].Text)
	}
}

func TestSaveSessionMessages_AutoTitle(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)
	ses := session.NewSession("zai", "default", "model")

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Please review my PR"}}},
	}

	if err := SaveSessionMessages(store, ses, msgs); err != nil {
		t.Fatalf("SaveSessionMessages: %v", err)
	}

	if ses.Title != "Please review my PR" {
		t.Errorf("expected title 'Please review my PR', got %q", ses.Title)
	}

	// Title should also be persisted to disk.
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "Please review my PR" {
		t.Errorf("expected title on disk 'Please review my PR', got %q", loaded.Title)
	}
}

func TestSaveSessionMessages_AutoTitleTruncation(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)
	ses := session.NewSession("zai", "default", "model")

	longText := "This is a very long title that exceeds sixty characters and should be truncated with ellipsis"
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: longText}}},
	}

	if err := SaveSessionMessages(store, ses, msgs); err != nil {
		t.Fatalf("SaveSessionMessages: %v", err)
	}

	// Title should be truncated.
	runes := []rune(ses.Title)
	if len(runes) > 60 {
		t.Errorf("title should be at most 60 runes, got %d", len(runes))
	}
	if ses.Title == longText {
		t.Errorf("title should be truncated")
	}
}

func TestSaveSessionMessages_EmptyMessagesDeletes(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)
	ses := session.NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "temp"}}},
	}
	// Manually write to disk.
	store.Save(ses)
	store.AppendMetaToDisk(ses)
	store.AppendMessagesBatchToDisk(ses, ses.Messages)

	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}

	// SaveSessionMessages with empty messages should delete.
	if err := SaveSessionMessages(store, ses, nil); err != nil {
		t.Fatalf("SaveSessionMessages: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file should be deleted for empty messages")
	}
}

func TestSaveSessionMessages_NilInputs(t *testing.T) {
	if err := SaveSessionMessages(nil, nil, nil); err != nil {
		t.Errorf("SaveSessionMessages(nil,nil,nil) should return nil, got %v", err)
	}

	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)
	store, _ := session.NewJSONLStore(dir)

	if err := SaveSessionMessages(store, nil, nil); err != nil {
		t.Errorf("SaveSessionMessages(store,nil,nil) should return nil, got %v", err)
	}
}

func TestSaveSessionMessages_DoesNotDestroyExistingData(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)
	ses := session.NewSession("zai", "default", "model")

	// Write initial messages.
	msgs1 := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "first"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "reply1"}}},
	}
	if err := SaveSessionMessages(store, ses, msgs1); err != nil {
		t.Fatalf("first SaveSessionMessages: %v", err)
	}

	// Call SaveSessionMessages again with only the first message (simulating
	// a compacted / partial message list). This should NOT destroy the
	// previously written second message on disk.
	msgs2 := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "second call"}}},
	}
	if err := SaveSessionMessages(store, ses, msgs2); err != nil {
		t.Fatalf("second SaveSessionMessages: %v", err)
	}

	// Load from disk — should have all 3 messages (incremental append).
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("expected 3 messages (incremental append), got %d", len(loaded.Messages))
	}
	// Verify messages are in order.
	expectedTexts := []string{"first", "reply1", "second call"}
	for i, want := range expectedTexts {
		if i >= len(loaded.Messages) {
			break
		}
		got := loaded.Messages[i].Content[0].Text
		if got != want {
			t.Errorf("message %d: expected %q, got %q", i, want, got)
		}
	}
}

func TestSaveSessionMessages_PreservesExistingTitle(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)
	ses := session.NewSession("zai", "default", "model")
	ses.Title = "Custom Title"

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}

	if err := SaveSessionMessages(store, ses, msgs); err != nil {
		t.Fatalf("SaveSessionMessages: %v", err)
	}

	// Title should not be overwritten.
	if ses.Title != "Custom Title" {
		t.Errorf("expected title 'Custom Title', got %q", ses.Title)
	}
}
