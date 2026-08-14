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
	interacted, err := store.HasUserInteractionOnDisk(id)
	if err != nil {
		t.Fatalf("HasUserInteractionOnDisk: %v", err)
	}
	if !interacted {
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

// TestHasUserInteractionOnDisk_UnreadableFileNotDeleted is the regression test
// for #291: when the session file cannot be opened (EACCES, or a write gap
// from another ggcode instance), the check must report unknown (non-nil
// error) and callers must keep the session instead of deleting it.
func TestHasUserInteractionOnDisk_UnreadableFileNotDeleted(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "unreadable-session"
	writeOldUserMessageJSONL(t, dir, id)

	// Make the file unreadable (chmod 0000). Running as root would defeat
	// this; skip in that case.
	if err := os.Chmod(filepath.Join(dir, id+".jsonl"), 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, id+".jsonl"), 0o600) }()

	interacted, herr := store.HasUserInteractionOnDisk(id)
	if herr == nil {
		// Root (or equivalent) can still read 0000 files — the error path is
		// not reachable in this environment.
		t.Skip("open unexpectedly succeeded (running as root?) — error path not reachable")
	}
	if interacted {
		t.Error("on open failure the check must report false + error, not true")
	}

	// Unknown state must keep the session.
	if store.WillCleanupIfEmpty(&Session{ID: id}) {
		t.Error("WillCleanupIfEmpty must be false when on-disk state is unknown (#291)")
	}
}

// TestHasUserInteractionOnDisk_OversizedLineConservativelyKept reproduces the
// #291 scenario where a user pastes a huge blob (>10MB) into a single JSONL
// message: bufio.Scanner hits ErrTooLong and previously Scan() returned false
// early, collapsing to "no user interaction" and deleting the session.
func TestHasUserInteractionOnDisk_OversizedLineConservativelyKept(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "oversized-line-session"
	path := filepath.Join(dir, id+".jsonl")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	now := time.Now()

	// 1. Normal user message BEFORE the oversized line.
	if err := enc.Encode(jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:   "small-user",
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "hello before the paste",
			}},
		},
		Timestamp: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// 2. A single message whose text exceeds the 10MB scanner buffer.
	huge := make([]byte, 10*1024*1024+1024)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := enc.Encode(jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:   "huge-user",
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: string(huge),
			}},
		},
		Timestamp: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// ErrTooLong must be treated conservatively: keep the session.
	interacted, herr := store.HasUserInteractionOnDisk(id)
	if herr != nil {
		t.Fatalf("oversized line should be handled without surfacing an error: %v", herr)
	}
	if !interacted {
		t.Error("oversized line must be conservatively reported as user interaction (#291)")
	}

	// And the session must not be marked for cleanup.
	if store.WillCleanupIfEmpty(&Session{ID: id}) {
		t.Error("WillCleanupIfEmpty must be false for a session with an oversized line (#291)")
	}
	if err := store.CleanupIfEmpty(&Session{ID: id}); err != nil {
		t.Fatalf("CleanupIfEmpty: %v", err)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("session file with oversized line must NOT be deleted: %v", serr)
	}
}
