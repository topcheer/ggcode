package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestSwitchToSessionUpdatesCurrentSession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)

	oldSes := session.NewSession("zai", "default", "model")
	oldSes.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old message"}}},
	}
	_ = store.Save(oldSes)
	_ = store.AppendMetaToDisk(oldSes)
	_ = store.AppendMessagesBatchToDisk(oldSes, oldSes.Messages)

	newSes := session.NewSession("zai", "default", "model")

	m := newTestModel()
	m.sessionStore = store
	m.SetSession(oldSes, store)

	var callbackSession *session.Session
	m.sessionUpdateCallback = func(s *session.Session) {
		callbackSession = s
	}

	m.SetSession(newSes, store)

	if callbackSession == nil {
		t.Fatal("sessionUpdateCallback was not called")
	}
	if callbackSession.ID != newSes.ID {
		t.Errorf("expected callback session %s, got %s", newSes.ID, callbackSession.ID)
	}

	if m.Session().ID != newSes.ID {
		t.Errorf("expected m.session %s, got %s", newSes.ID, m.Session().ID)
	}
}

func TestPersistHandlerRoutesToCorrectSession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := session.NewJSONLStore(dir)

	ses1 := session.NewSession("zai", "default", "model")
	ses2 := session.NewSession("zai", "default", "model")

	_ = store.Save(ses1)
	_ = store.Save(ses2)

	msg1 := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "message for ses1"}},
	}
	_ = store.AppendMessageToDisk(ses1, msg1)

	msg2 := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "message for ses2"}},
	}
	_ = store.AppendMessageToDisk(ses2, msg2)

	loaded1, err := store.Load(ses1.ID)
	if err != nil {
		t.Fatalf("Load ses1: %v", err)
	}
	if len(loaded1.Messages) != 1 {
		t.Errorf("ses1: expected 1 message, got %d", len(loaded1.Messages))
	}
	if loaded1.Messages[0].Content[0].Text != "message for ses1" {
		t.Errorf("ses1: expected 'message for ses1', got %q", loaded1.Messages[0].Content[0].Text)
	}

	loaded2, err := store.Load(ses2.ID)
	if err != nil {
		t.Fatalf("Load ses2: %v", err)
	}
	if len(loaded2.Messages) != 1 {
		t.Errorf("ses2: expected 1 message, got %d", len(loaded2.Messages))
	}
	if loaded2.Messages[0].Content[0].Text != "message for ses2" {
		t.Errorf("ses2: expected 'message for ses2', got %q", loaded2.Messages[0].Content[0].Text)
	}

	path1 := filepath.Join(dir, ses1.ID+".jsonl")
	path2 := filepath.Join(dir, ses2.ID+".jsonl")
	info1, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("ses1 file: %v", err)
	}
	info2, err := os.Stat(path2)
	if err != nil {
		t.Fatalf("ses2 file: %v", err)
	}
	if info1.Size() == 0 || info2.Size() == 0 {
		t.Errorf("both session files should have content: ses1=%d ses2=%d", info1.Size(), info2.Size())
	}
}
