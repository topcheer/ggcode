package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// #4218: persistUserMessageRemoval must tolerate a nil persistSession snapshot
// (ClearSession sets it to nil) WITHOUT dereferencing it while holding b.mu -
// a panic under lock would deadlock the whole ChatBridge if any upstream
// recover() swallows it. The zero-value bridge is safe here: nil map reads
// return zero, and sessionLockMismatchLocked handles nil ses/lock.
func TestPersistUserMessageRemovalNilSessionNoPanic(t *testing.T) {
	b := &ChatBridge{}
	b.mu.Lock()
	b.persistSession = nil // simulate session switch racing the vision-fallback path
	b.mu.Unlock()

	// Must return silently, not panic (nil deref in map access was the old bug).
	b.persistUserMessageRemoval()
}

// With a live session and JSONL store the tombstone must still be appended
// (regression guard: the nil fix must not swallow legitimate removals).
func TestPersistUserMessageRemovalLiveSessionStillPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := session.NewSession("test", "https://example.invalid", "m")
	if err := store.AppendMessageToDisk(ses, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "original image"}},
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	b := &ChatBridge{}
	b.mu.Lock()
	b.sessionStore = store
	b.persistSession = ses
	b.mu.Unlock()

	b.persistUserMessageRemoval()

	raw, err := os.ReadFile(filepath.Join(dir, ses.ID+".jsonl"))
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if !strings.Contains(string(raw), `"message_remove"`) {
		t.Fatalf("expected message_remove tombstone in journal, got:\n%s", raw)
	}
}
