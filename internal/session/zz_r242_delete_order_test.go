package session

// R242 review fix: Delete must remove the index entry BEFORE the file.
// The old order left a permanent ghost row when removeFromIndex failed
// after the file was already gone - List() kept showing the session,
// loading failed, and no repair pass could self-heal it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestR242_DeleteIndexFirstNoGhost(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_r242_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	msg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "r242 ghost guard"}}}
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete(ses.ID); err != nil {
		t.Fatal(err)
	}

	// Index entry gone...
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range list {
		if l.ID == ses.ID {
			t.Fatal("index entry must be gone after Delete")
		}
	}
	// ...and the file is gone too (both legs of the two-step delete).
	if _, err := os.Stat(filepath.Join(dir, ses.ID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("session file must be removed, stat err=%v", err)
	}
}
