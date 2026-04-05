package session

import (
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSaveLoad(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Test Session"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Hi there!"}}},
	}

	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Test Session" {
		t.Fatalf("title mismatch: %s", loaded.Title)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("message count: %d", len(loaded.Messages))
	}
}

func TestList(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses1 := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses1.Title = "First"
	store.Save(ses1)
	// Ensure different second to get unique ID
	time.Sleep(1100 * time.Millisecond)
	ses2 := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses2.Title = "Second"
	store.Save(ses2)

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list count: %d", len(list))
	}
}

func TestAppendMessage(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	store.Save(ses)

	msg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Follow up"}}}
	if err := store.AppendMessage(ses, msg); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := store.Load(ses.ID)
	if len(reloaded.Messages) != 1 {
		t.Fatalf("after append, messages=%d", len(reloaded.Messages))
	}
}

func TestExportMarkdown(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Test"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	store.Save(ses)

	md, err := store.ExportMarkdown(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(md) < 10 {
		t.Fatalf("markdown too short: %d", len(md))
	}
}

func TestDelete(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	store.Save(ses)

	if err := store.Delete(ses.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ses.ID); err == nil {
		t.Fatal("load after delete should fail")
	}
}

func TestCleanupOlderThan(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	store.Save(ses)

	removed, err := store.CleanupOlderThan(time.Now().Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want 1", removed)
	}
}
