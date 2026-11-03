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

func TestCleanupIfEmpty_WindowedLoadKeepsOldUserMessages(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "windowed-old-user"
	writeOldUserMessageJSONL(t, dir, id)

	loaded, err := store.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected windowed load to keep only the final message, got %d", len(loaded.Messages))
	}
	if loaded.HasUserInteraction() {
		t.Fatal("in-memory windowed session should report no user interaction (bug precondition)")
	}

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

func TestHasUserInteractionOnDisk_UnreadableFileNotDeleted(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	id := "unreadable-session"
	writeOldUserMessageJSONL(t, dir, id)

	if err := os.Chmod(filepath.Join(dir, id+".jsonl"), 0o000); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, id+".jsonl"), 0o600) }()

	interacted, herr := store.HasUserInteractionOnDisk(id)
	if herr == nil {

		t.Skip("open unexpectedly succeeded (running as root?) — error path not reachable")
	}
	if interacted {
		t.Error("on open failure the check must report false + error, not true")
	}

	if store.WillCleanupIfEmpty(&Session{ID: id}) {
		t.Error("WillCleanupIfEmpty must be false when on-disk state is unknown (#291)")
	}
}

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

	interacted, herr := store.HasUserInteractionOnDisk(id)
	if herr != nil {
		t.Fatalf("oversized line should be handled without surfacing an error: %v", herr)
	}
	if !interacted {
		t.Error("oversized line must be conservatively reported as user interaction (#291)")
	}

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
