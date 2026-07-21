package session

import (
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestAppendMessageToDisk_DebounceIndexUpdate verifies that AppendMessageToDisk
// debounces index updates — the first message updates the index, subsequent
// messages within the debounce window skip it, and messages after the window
// expiry update it again.
func TestAppendMessageToDisk_DebounceIndexUpdate(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_debounce_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := NewSession("test", "default", "model")
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "hello"}},
	}

	// First AppendMessageToDisk must update the index.
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatalf("first AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	firstUpdate, ok := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()
	if !ok {
		t.Fatal("expected lastIndexUpdate entry after first append")
	}

	// Second append within debounce window — should NOT update the index.
	msg2 := provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "world"}},
	}
	if err := store.AppendMessageToDisk(ses, msg2); err != nil {
		t.Fatalf("second AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	secondUpdate := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()

	// Timer should be unchanged (debounced, not rewritten).
	if !secondUpdate.Equal(firstUpdate) {
		t.Errorf("expected index update to be debounced (same timestamp), first=%v second=%v", firstUpdate, secondUpdate)
	}

	// Force the debounce window to expire.
	store.mu.Lock()
	store.lastIndexUpdate[ses.ID] = time.Now().Add(-indexUpdateDebounce - time.Second)
	store.mu.Unlock()

	// Third append after debounce expiry — should update the index again.
	msg3 := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "again"}},
	}
	if err := store.AppendMessageToDisk(ses, msg3); err != nil {
		t.Fatalf("third AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	thirdUpdate := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()
	if !thirdUpdate.After(secondUpdate) {
		t.Errorf("expected index update after debounce expiry, second=%v third=%v", secondUpdate, thirdUpdate)
	}

	// All 3 messages must be in the session file regardless of index debounce.
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("expected 3 messages in session, got %d", len(loaded.Messages))
	}
}

// TestAppendMetaToDisk_ResetsDebounce verifies that AppendMetaToDisk resets
// the debounce timer so the next AppendMessageToDisk within the window
// skips the index update.
func TestAppendMetaToDisk_ResetsDebounce(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_meta_debounce_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := NewSession("test", "default", "model")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	// Meta write updates index and sets debounce timer.
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}

	store.mu.Lock()
	metaUpdate := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()

	// Immediate message append should be debounced (same timestamp).
	msg := provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "response"}},
	}
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatalf("AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	msgUpdate := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()

	if !msgUpdate.Equal(metaUpdate) {
		t.Errorf("expected AppendMessageToDisk to be debounced after AppendMetaToDisk, meta=%v msg=%v", metaUpdate, msgUpdate)
	}
}
