package session

import (
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

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

	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatalf("first AppendMessageToDisk: %v", err)
	}

	store.mu.Lock()
	firstUpdate, ok := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()
	if !ok {
		t.Fatal("expected lastIndexUpdate entry after first append")
	}

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

	if !secondUpdate.Equal(firstUpdate) {
		t.Errorf("expected index update to be debounced (same timestamp), first=%v second=%v", firstUpdate, secondUpdate)
	}

	store.mu.Lock()
	store.lastIndexUpdate[ses.ID] = time.Now().Add(-indexUpdateDebounce - time.Second)
	store.mu.Unlock()

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

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("expected 3 messages in session, got %d", len(loaded.Messages))
	}
}

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

	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatalf("AppendMetaToDisk: %v", err)
	}

	store.mu.Lock()
	metaUpdate := store.lastIndexUpdate[ses.ID]
	store.mu.Unlock()

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
