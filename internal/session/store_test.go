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
	ses.Workspace = "/tmp/workspace-a"
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
	if loaded.Workspace != "/tmp/workspace-a" {
		t.Fatalf("workspace mismatch: %s", loaded.Workspace)
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
	ses1.Workspace = "/tmp/workspace-a"
	store.Save(ses1)
	// Ensure different second to get unique ID
	time.Sleep(1100 * time.Millisecond)
	ses2 := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses2.Title = "Second"
	ses2.Workspace = "/tmp/workspace-b"
	store.Save(ses2)

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list count: %d", len(list))
	}
	if list[0].Workspace == "" || list[1].Workspace == "" {
		t.Fatalf("expected workspace metadata in list: %+v", list)
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

func TestAppendCheckpoint(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "test-checkpoint"

	// Save initial session with some messages
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	}
	store.Save(ses)

	// Append a checkpoint with compacted messages
	compacted := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]\nUser asked about X"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "follow-up"}}},
	}
	err := store.AppendCheckpoint(ses, compacted, 50)
	if err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}

	// Append messages after checkpoint
	store.AppendMessage(ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "response after checkpoint"}}})
	store.AppendMessage(ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "new question"}}})

	// Load and verify
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have: 2 checkpoint messages + 2 post-checkpoint messages = 4
	if len(loaded.Messages) != 4 {
		t.Fatalf("expected 4 messages (2 checkpoint + 2 post-checkpoint), got %d", len(loaded.Messages))
	}

	// First message should be from checkpoint
	if loaded.Messages[0].Role != "system" {
		t.Fatalf("expected first message role 'system', got '%s'", loaded.Messages[0].Role)
	}
	// Last message should be the post-checkpoint user message
	lastMsg := loaded.Messages[len(loaded.Messages)-1]
	if lastMsg.Role != "user" {
		t.Fatalf("expected last message role 'user', got '%s'", lastMsg.Role)
	}
	if lastMsg.Content[0].Text != "new question" {
		t.Fatalf("expected last message text 'new question', got '%s'", lastMsg.Content[0].Text)
	}
}

func TestLoadWithoutCheckpoint(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg1"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "msg2"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg3"}}},
	}
	store.Save(ses)

	// Load without any checkpoint — should return all 3 messages
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("expected 3 messages (no checkpoint), got %d", len(loaded.Messages))
	}
}

func TestLoadWithMultipleCheckpoints(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "multi-checkpoint"

	// Initial save
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "original msg"}}},
	}
	store.Save(ses)

	// First checkpoint
	cp1 := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary v1"}}},
	}
	store.AppendCheckpoint(ses, cp1, 100)

	// Messages after first checkpoint
	store.AppendMessage(ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg after cp1"}}})

	// Second checkpoint (should supersede first)
	cp2 := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary v2"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "retained context"}}},
	}
	store.AppendCheckpoint(ses, cp2, 80)

	// Messages after second checkpoint
	store.AppendMessage(ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "final response"}}})

	// Load — should use second checkpoint + 1 post-checkpoint message = 3 total
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.Messages) != 3 {
		t.Fatalf("expected 3 messages (2 from cp2 + 1 post-checkpoint), got %d", len(loaded.Messages))
	}

	// First should be "summary v2"
	if loaded.Messages[0].Content[0].Text != "summary v2" {
		t.Fatalf("expected first msg 'summary v2', got '%s'", loaded.Messages[0].Content[0].Text)
	}
	// Last should be "final response"
	if loaded.Messages[2].Content[0].Text != "final response" {
		t.Fatalf("expected last msg 'final response', got '%s'", loaded.Messages[2].Content[0].Text)
	}
}
