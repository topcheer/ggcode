package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// writeOldUserMessageJSONL writes a session JSONL file reproducing the #254
// scenario: recentMessageThreshold+ messages whose user messages all sit more
// than RecentMessageWindow before the last message, so time-windowed loading
// drops every user message from ses.Messages.
func writeOldUserMessageJSONL(t *testing.T, dir, id string) {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	now := time.Now()
	// 510 user messages clustered 49h..48h ago (well outside the 24h window).
	for i := 0; i < recentMessageThreshold+10; i++ {
		rec := jsonlRecord{
			Type: "message",
			Message: &provider.Message{
				ID:      fmt.Sprintf("user-msg-%d", i),
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("user message %d", i)}},
			},
			Timestamp: now.Add(-49 * time.Hour).Add(time.Duration(i) * time.Second),
		}
		if err := enc.Encode(rec); err != nil {
			t.Fatal(err)
		}
	}
	// Boundary assistant message just outside the window. The window cutoff
	// keeps the record whose end offset equals the cutoff byte offset, so
	// without this separator the last user message would be retained by that
	// boundary and the bug precondition would not hold.
	boundary := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "boundary-msg",
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "boundary"}},
		},
		Timestamp: now.Add(-25*time.Hour - 30*time.Minute),
	}
	if err := enc.Encode(boundary); err != nil {
		t.Fatal(err)
	}
	// Final message 1h ago (e.g. cron-appended) — sets the window boundary so
	// all user messages above fall outside the 24h RecentMessageWindow.
	final := jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:      "final-msg",
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "scheduled note"}},
		},
		Timestamp: now.Add(-1 * time.Hour),
	}
	if err := enc.Encode(final); err != nil {
		t.Fatal(err)
	}
}

// TestCleanupIfEmpty_WindowedLoadKeepsOldUserMessages is the regression test
// for #254: time-windowed loading drops user messages older than
// RecentMessageWindow from ses.Messages, which previously made
// CleanupIfEmpty believe the session had no user interaction and silently
// delete the entire JSONL file on exit.
func TestCleanupIfEmpty_WindowedLoadKeepsOldUserMessages(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "windowed-old-user"
	writeOldUserMessageJSONL(t, dir, id)

	// Sanity check (bug precondition): windowed load drops all user messages.
	// The boundary record immediately before the final message is retained
	// (its end offset equals the cutoff), so boundary + final = 2 messages.
	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected windowed load to keep boundary + final message, got %d", len(loaded.Messages))
	}
	if loaded.HasUserInteraction() {
		t.Fatal("in-memory windowed session should report no user interaction (bug precondition)")
	}

	// The fix: file-level checks must see the historical user interaction.
	if !store.HasUserInteractionOnDisk(id) {
		t.Error("HasUserInteractionOnDisk should find user messages older than the load window")
	}
	if store.WillCleanupIfEmpty(loaded) {
		t.Error("WillCleanupIfEmpty should be false — user messages exist on disk")
	}
	if err := store.CleanupIfEmpty(loaded); err != nil {
		t.Fatalf("CleanupIfEmpty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); err != nil {
		t.Errorf("session file must NOT be deleted when user messages exist outside the load window: %v", err)
	}
}

// TestHasUserInteractionOnDisk_EmptyFile ensures empty sessions (no user
// messages) are still cleaned up after the #254 fix.
func TestHasUserInteractionOnDisk_EmptyFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if store.HasUserInteractionOnDisk(ses.ID) {
		t.Error("empty session file should have no user interaction on disk")
	}
	if !store.WillCleanupIfEmpty(ses) {
		t.Error("WillCleanupIfEmpty should be true for an empty session")
	}
	if err := store.CleanupIfEmpty(ses); err != nil {
		t.Fatalf("CleanupIfEmpty: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ses.ID+".jsonl")); !os.IsNotExist(err) {
		t.Error("empty session file should be deleted by CleanupIfEmpty")
	}
}
