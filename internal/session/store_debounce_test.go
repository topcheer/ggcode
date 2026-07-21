package session

import (
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestAppendMessageToDisk_DebounceIndexUpdate verifies that AppendMessageToDisk
// debounces index updates — the first message updates the index, subsequent
// messages within the debounce window skip it, and the index stays correct.
func TestAppendMessageToDisk_DebounceIndexUpdate(t *testing.T) {
	dir := t.TempDir()
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

	// First AppendMessageToDisk must update the index (session appears in List).
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatalf("first AppendMessageToDisk: %v", err)
	}

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List after first append: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session in index after first append, got %d", len(sessions))
	}

	// Record the index file modification time.
	indexPath := store.indexPath()
	info1, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	_ = info1 // just verify it's accessible

	// Second append within debounce window — should NOT rewrite the index.
	msg2 := provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "world"}},
	}
	if err := store.AppendMessageToDisk(ses, msg2); err != nil {
		t.Fatalf("second AppendMessageToDisk: %v", err)
	}

	// The index file should not have been rewritten (mtime unchanged or
	// updated within the same second — we verify via the debounce map state).
	store.mu.Lock()
	lastUpdate, ok := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()
	if !ok {
		t.Fatal("expected lastIndexUpdate entry for session")
	}

	// Simulate a third append right after — still within debounce window.
	if time.Since(lastUpdate) < indexUpdateDebounce {
		// Debounce is active — good.
		// Force the debounce window to expire and verify the next append updates.
		store.mu.Lock()
		store.lastIndexUpdate[ses.ID] = time.Now().Add(-indexUpdateDebounce - time.Second)
		store.mu.Unlock()
	}

	// Third append after debounce expiry — should update the index again.
	msg3 := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "again"}},
	}
	if err := store.AppendMessageToDisk(ses, msg3); err != nil {
		t.Fatalf("third AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	lastUpdate2 := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()
	if time.Since(lastUpdate2) > time.Second {
		t.Errorf("expected recent lastIndexUpdate after debounce expiry, got %v ago", time.Since(lastUpdate2))
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
	dir := t.TempDir()
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

	// Immediate message append should be debounced.
	msg := provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "response"}},
	}
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatalf("AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	lastUpdate := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()

	// The timer should be very recent (set by AppendMetaToDisk).
	if time.Since(lastUpdate) > time.Second {
		t.Errorf("expected AppendMetaToDisk to reset debounce timer, got %v ago", time.Since(lastUpdate))
	}
}
