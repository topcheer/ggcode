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

func TestLoadWithEmptyCheckpoint(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "empty-checkpoint"
	ses.Workspace = "/tmp/empty-ws"

	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
	}
	store.Save(ses)

	// Append checkpoint with empty messages — simulates summarize producing
	// zero retained messages (edge case).
	err := store.AppendCheckpoint(ses, []provider.Message{}, 0)
	if err != nil {
		t.Fatalf("AppendCheckpoint with empty messages failed: %v", err)
	}

	// Append a post-checkpoint message
	store.AppendMessage(ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "after empty cp"}}})

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Messages: only the post-checkpoint message (empty checkpoint contributes 0)
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message (empty checkpoint + 1 post-checkpoint), got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "assistant" {
		t.Fatalf("expected role 'assistant', got '%s'", loaded.Messages[0].Role)
	}
	if loaded.Messages[0].Content[0].Text != "after empty cp" {
		t.Fatalf("expected 'after empty cp', got '%s'", loaded.Messages[0].Content[0].Text)
	}

	// Metadata must survive past the checkpoint
	if loaded.Title != "empty-checkpoint" {
		t.Fatalf("title lost after empty checkpoint: got '%s'", loaded.Title)
	}
	if loaded.Workspace != "/tmp/empty-ws" {
		t.Fatalf("workspace lost after empty checkpoint: got '%s'", loaded.Workspace)
	}
	if loaded.Vendor != "zai" {
		t.Fatalf("vendor lost: got '%s'", loaded.Vendor)
	}
	if loaded.Endpoint != "cn-coding-openai" {
		t.Fatalf("endpoint lost: got '%s'", loaded.Endpoint)
	}
	if loaded.Model != "glm-5-turbo" {
		t.Fatalf("model lost: got '%s'", loaded.Model)
	}
}

func TestLoadCheckpointWithNoPostCheckpointMessages(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "cp-only"
	ses.Workspace = "/tmp/cp-ws"

	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "original"}}},
	}
	store.Save(ses)

	// Append checkpoint — no messages after it, simulating a crash
	// immediately after compaction.
	compacted := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary only"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "retained user context"}}},
	}
	store.AppendCheckpoint(ses, compacted, 10)

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Must have exactly the 2 checkpoint messages — no more, no less
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages from checkpoint, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Role != "system" {
		t.Fatalf("expected first role 'system', got '%s'", loaded.Messages[0].Role)
	}
	if loaded.Messages[0].Content[0].Text != "summary only" {
		t.Fatalf("expected first text 'summary only', got '%s'", loaded.Messages[0].Content[0].Text)
	}
	if loaded.Messages[1].Role != "user" {
		t.Fatalf("expected second role 'user', got '%s'", loaded.Messages[1].Role)
	}
	if loaded.Messages[1].Content[0].Text != "retained user context" {
		t.Fatalf("expected second text 'retained user context', got '%s'", loaded.Messages[1].Content[0].Text)
	}

	// Metadata preserved through checkpoint
	if loaded.Title != "cp-only" {
		t.Fatalf("title lost: got '%s'", loaded.Title)
	}
	if loaded.Workspace != "/tmp/cp-ws" {
		t.Fatalf("workspace lost: got '%s'", loaded.Workspace)
	}
}

func TestAppendCheckpointUpdatesIndex(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "index-test"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg"}}},
	}
	store.Save(ses)

	originalUpdatedAt := ses.UpdatedAt
	time.Sleep(10 * time.Millisecond) // ensure time difference

	compacted := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summarized"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post-summary"}}},
	}
	if err := store.AppendCheckpoint(ses, compacted, 10); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}

	// Verify index was updated (UpdatedAt should be newer)
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	var found *Session
	for _, s := range list {
		if s.ID == ses.ID {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("session not found in index")
	}
	if !found.UpdatedAt.After(originalUpdatedAt) {
		t.Fatalf("index UpdatedAt not updated after checkpoint: original=%v current=%v", originalUpdatedAt, found.UpdatedAt)
	}
	// Title must survive through index round-trip
	if found.Title != "index-test" {
		t.Fatalf("title lost in index: got '%s'", found.Title)
	}

	// Verify MsgCount reflects checkpoint content by loading via index ID
	loaded, err := store.Load(found.ID)
	if err != nil {
		t.Fatalf("Load after index update failed: %v", err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 checkpoint messages on reload, got %d", len(loaded.Messages))
	}
}

func TestAppendCheckpointFileNotExist(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "no-file"

	// Do NOT call Save — the JSONL file does not exist yet.
	// AppendCheckpoint should create it (O_CREATE flag).
	compacted := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "from scratch"}}},
	}
	if err := store.AppendCheckpoint(ses, compacted, 5); err != nil {
		t.Fatalf("AppendCheckpoint on non-existent file should succeed, got: %v", err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 checkpoint message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content[0].Text != "from scratch" {
		t.Fatalf("expected 'from scratch', got '%s'", loaded.Messages[0].Content[0].Text)
	}
}
