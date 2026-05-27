package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
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
	ses.TokenUsage = provider.TokenUsage{InputTokens: 1200, OutputTokens: 340, CacheRead: 800, CacheWrite: 64}
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
	if loaded.TokenUsage != ses.TokenUsage {
		t.Fatalf("token usage mismatch: %+v", loaded.TokenUsage)
	}
}

func TestList(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses1 := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses1.Title = "First"
	ses1.Workspace = "/tmp/workspace-a"
	ses1.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	store.Save(ses1)
	// Ensure different second to get unique ID
	time.Sleep(1100 * time.Millisecond)
	ses2 := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses2.Title = "Second"
	ses2.Workspace = "/tmp/workspace-b"
	ses2.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}}}
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

func TestSaveLoadTunnelEvents(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Tunnel Event Session"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	ses.TunnelEvents = []TunnelEvent{
		{EventID: "ev-000000001", Type: "user_message", Data: json.RawMessage(`{"text":"Hello"}`)},
		{EventID: "ev-000000002", StreamID: "msg-1", Type: "text", Data: json.RawMessage(`{"id":"msg-1","chunk":"Hi"}`)},
	}
	ses.TunnelEventsComplete = true

	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.TunnelEvents) != 2 {
		t.Fatalf("expected 2 tunnel events, got %d", len(loaded.TunnelEvents))
	}
	if loaded.TunnelEvents[0].EventID != "ev-000000001" || loaded.TunnelEvents[1].StreamID != "msg-1" {
		t.Fatalf("unexpected tunnel events: %+v", loaded.TunnelEvents)
	}
	if !loaded.TunnelEventsComplete {
		t.Fatal("expected tunnel event completeness flag to survive load")
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
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "cleanup test"}}}}
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

func TestSaveSkipsEmptySession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	// No messages — Save should not create a file.
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty session file should not exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); !os.IsNotExist(err) {
		t.Errorf("empty session save should not create an index file")
	}
	// List should not include it
	list, _ := store.List()
	if len(list) != 0 {
		t.Errorf("List returned %d sessions, want 0", len(list))
	}
}

func TestSaveDeletesPreviouslySavedEmptySession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	// Save a session with messages first.
	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}}
	store.Save(ses)
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("file should exist after save with messages")
	}
	// Now save the same session with messages cleared (simulating empty exit).
	ses.Messages = nil
	store.Save(ses)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be deleted after saving empty session")
	}
}

func TestHasUserInteraction(t *testing.T) {
	ses := NewSession("zai", "default", "model")
	if ses.HasUserInteraction() {
		t.Error("new session should have no user interaction")
	}
	// Assistant-only message doesn't count
	ses.Messages = []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if ses.HasUserInteraction() {
		t.Error("assistant-only session should have no user interaction")
	}
	// User message with text counts
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if !ses.HasUserInteraction() {
		t.Error("session with user text should have user interaction")
	}
	// User message with empty text doesn't count
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "  "}}}}
	if ses.HasUserInteraction() {
		t.Error("session with whitespace-only user text should have no user interaction")
	}
	// User message with tool_use doesn't count as text interaction
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", ToolName: "test"}}}}
	if ses.HasUserInteraction() {
		t.Error("session with only tool results should have no user interaction")
	}
}

func TestEnsureMetaSkipsEmptySession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	// No messages — EnsureMeta should not create file.
	if err := store.EnsureMeta(ses); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("empty session should not have meta file")
	}
}

func TestEnsureMetaCreatesFileForSessionWithMessages(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if err := store.EnsureMeta(ses); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("session with messages should have meta file")
	}
}

func TestAppendMetaToDiskPersistsTokenUsage(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	ses.TokenUsage = provider.TokenUsage{InputTokens: 42, OutputTokens: 9, CacheRead: 18, CacheWrite: 3}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TokenUsage != ses.TokenUsage {
		t.Fatalf("token usage mismatch after meta append: %+v", loaded.TokenUsage)
	}
}

func TestListCleansUpEmptySessions(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	// Create a session with messages.
	ses1 := NewSession("zai", "default", "model")
	ses1.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "keep me"}}}}
	store.Save(ses1)

	// Manually create an empty session file (simulating crash before cleanup).
	ses2 := NewSession("zai", "default", "model")
	// Write just a meta record (no messages).
	metaPath := filepath.Join(dir, ses2.ID+".jsonl")
	f, _ := os.Create(metaPath)
	json.NewEncoder(f).Encode(jsonlRecord{Type: "meta", SessionID: ses2.ID, Title: "empty"})
	f.Close()

	// Manually add to index.
	idx, _ := store.loadIndex()
	idx = append(idx, indexEntry{ID: ses2.ID, Title: "empty"})
	store.saveIndex(idx)

	// List should clean up the empty session.
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d sessions, want 1", len(list))
	}
	if list[0].ID != ses1.ID {
		t.Errorf("expected session %s, got %s", ses1.ID, list[0].ID)
	}
	// Empty session file should be gone.
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Error("empty session file should have been cleaned up")
	}
}

func TestDefaultDir_RespectsHomeOverride(t *testing.T) {
	orig := os.Getenv("HOME")
	defer os.Setenv("HOME", orig)

	tmp := t.TempDir()
	os.Setenv("HOME", tmp)

	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	want := tmp + "/.ggcode/sessions"
	if dir != want {
		t.Errorf("DefaultDir() = %q, want %q", dir, want)
	}
}

func TestAppendUsageEntry_PersistsAndLoads(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	entry1 := UsageEntry{
		Timestamp: time.Now().Truncate(time.Second),
		TurnIndex: 1,
		Model:     "glm-5-turbo",
		Vendor:    "zai",
		Endpoint:  "cn-coding-openai",
		Usage:     provider.TokenUsage{InputTokens: 500, OutputTokens: 100, CacheRead: 200},
	}
	entry2 := UsageEntry{
		Timestamp: time.Now().Truncate(time.Second),
		TurnIndex: 1,
		Model:     "glm-5-turbo",
		Vendor:    "zai",
		Endpoint:  "cn-coding-openai",
		Usage:     provider.TokenUsage{InputTokens: 300, OutputTokens: 80, CacheRead: 100},
	}
	entry3 := UsageEntry{
		Timestamp: time.Now().Truncate(time.Second),
		TurnIndex: 2,
		Model:     "glm-5-turbo",
		Vendor:    "zai",
		Endpoint:  "cn-coding-openai",
		Usage:     provider.TokenUsage{InputTokens: 600, OutputTokens: 150},
	}

	if err := store.AppendUsageEntry(ses, entry1); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsageEntry(ses, entry2); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsageEntry(ses, entry3); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.UsageHistory) != 3 {
		t.Fatalf("expected 3 usage entries, got %d", len(loaded.UsageHistory))
	}

	// Verify first entry
	if loaded.UsageHistory[0].TurnIndex != 1 {
		t.Errorf("entry 0 turn index: got %d, want 1", loaded.UsageHistory[0].TurnIndex)
	}
	if loaded.UsageHistory[0].Usage.InputTokens != 500 {
		t.Errorf("entry 0 input tokens: got %d, want 500", loaded.UsageHistory[0].Usage.InputTokens)
	}
	if loaded.UsageHistory[0].Usage.CacheRead != 200 {
		t.Errorf("entry 0 cache read: got %d, want 200", loaded.UsageHistory[0].Usage.CacheRead)
	}

	// Verify third entry (different turn)
	if loaded.UsageHistory[2].TurnIndex != 2 {
		t.Errorf("entry 2 turn index: got %d, want 2", loaded.UsageHistory[2].TurnIndex)
	}
	if loaded.UsageHistory[2].Usage.InputTokens != 600 {
		t.Errorf("entry 2 input tokens: got %d, want 600", loaded.UsageHistory[2].Usage.InputTokens)
	}

	// Verify metadata on entries
	if loaded.UsageHistory[0].Model != "glm-5-turbo" {
		t.Errorf("entry 0 model: got %q, want glm-5-turbo", loaded.UsageHistory[0].Model)
	}
	if loaded.UsageHistory[0].Vendor != "zai" {
		t.Errorf("entry 0 vendor: got %q, want zai", loaded.UsageHistory[0].Vendor)
	}
}

func TestAppendUsageEntry_SurvivesCheckpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	// Add usage entry before checkpoint
	entry1 := UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: 1,
		Usage:     provider.TokenUsage{InputTokens: 500, OutputTokens: 100},
	}
	if err := store.AppendUsageEntry(ses, entry1); err != nil {
		t.Fatal(err)
	}

	// Add checkpoint
	checkpointMsgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Hi!"}}},
	}
	if err := store.AppendCheckpoint(ses, checkpointMsgs, 50); err != nil {
		t.Fatal(err)
	}

	// Add usage entry after checkpoint
	entry2 := UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: 2,
		Usage:     provider.TokenUsage{InputTokens: 300, OutputTokens: 80},
	}
	if err := store.AppendUsageEntry(ses, entry2); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Usage entries before checkpoint are lost (checkpoint resets),
	// but entries after checkpoint should survive.
	if len(loaded.UsageHistory) != 1 {
		t.Fatalf("expected 1 usage entry after checkpoint, got %d", len(loaded.UsageHistory))
	}
	if loaded.UsageHistory[0].TurnIndex != 2 {
		t.Errorf("turn index: got %d, want 2", loaded.UsageHistory[0].TurnIndex)
	}
}

func TestSaveLoad_WithUsageHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	ses.UsageHistory = []UsageEntry{
		{Timestamp: time.Now().Truncate(time.Second), TurnIndex: 1, Model: "glm-5-turbo", Usage: provider.TokenUsage{InputTokens: 500, OutputTokens: 100}},
		{Timestamp: time.Now().Truncate(time.Second), TurnIndex: 2, Model: "glm-5-turbo", Usage: provider.TokenUsage{InputTokens: 300, OutputTokens: 80}},
	}

	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.UsageHistory) != 2 {
		t.Fatalf("expected 2 usage entries, got %d", len(loaded.UsageHistory))
	}
	if loaded.UsageHistory[0].TurnIndex != 1 || loaded.UsageHistory[1].TurnIndex != 2 {
		t.Errorf("unexpected turn indices: %d, %d", loaded.UsageHistory[0].TurnIndex, loaded.UsageHistory[1].TurnIndex)
	}
	if loaded.UsageHistory[0].Usage.InputTokens != 500 {
		t.Errorf("entry 0 input tokens: got %d, want 500", loaded.UsageHistory[0].Usage.InputTokens)
	}
}

func TestAppendMetric_PersistsAndLoads(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	m1 := metrics.MetricEvent{
		Timestamp: time.Now().Truncate(time.Second),
		TurnIndex: 1,
		Type:      "llm",
		TTFT:      150 * time.Millisecond,
		ThinkTime: 200 * time.Millisecond,
		Duration:  2 * time.Second,
		Model:     "glm-5-turbo",
		Vendor:    "zai",
		Endpoint:  "cn-coding-openai",
	}
	m2 := metrics.MetricEvent{
		Timestamp:    time.Now().Truncate(time.Second),
		TurnIndex:    1,
		Type:         "tool",
		ToolName:     "read_file",
		ToolSuccess:  true,
		ToolDuration: 50 * time.Millisecond,
	}
	m3 := metrics.MetricEvent{
		Timestamp:    time.Now().Truncate(time.Second),
		TurnIndex:    1,
		Type:         "tool",
		ToolName:     "run_command",
		ToolSuccess:  false,
		ToolError:    "exit status 1",
		ToolDuration: 500 * time.Millisecond,
	}

	if err := store.AppendMetric(ses, m1); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMetric(ses, m2); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMetric(ses, m3); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Metrics) != 3 {
		t.Fatalf("expected 3 metric events, got %d", len(loaded.Metrics))
	}

	// LLM metric
	if loaded.Metrics[0].Type != "llm" {
		t.Errorf("metric 0 type: got %q, want llm", loaded.Metrics[0].Type)
	}
	if loaded.Metrics[0].TTFT != 150*time.Millisecond {
		t.Errorf("metric 0 TTFT: got %v, want 150ms", loaded.Metrics[0].TTFT)
	}
	if loaded.Metrics[0].ThinkTime != 200*time.Millisecond {
		t.Errorf("metric 0 think time: got %v, want 200ms", loaded.Metrics[0].ThinkTime)
	}
	if loaded.Metrics[0].Duration != 2*time.Second {
		t.Errorf("metric 0 duration: got %v, want 2s", loaded.Metrics[0].Duration)
	}

	// Tool success metric
	if loaded.Metrics[1].Type != "tool" {
		t.Errorf("metric 1 type: got %q, want tool", loaded.Metrics[1].Type)
	}
	if loaded.Metrics[1].ToolName != "read_file" {
		t.Errorf("metric 1 tool: got %q, want read_file", loaded.Metrics[1].ToolName)
	}
	if !loaded.Metrics[1].ToolSuccess {
		t.Error("metric 1 should be success")
	}

	// Tool failure metric
	if loaded.Metrics[2].ToolSuccess {
		t.Error("metric 2 should be failure")
	}
	if loaded.Metrics[2].ToolError != "exit status 1" {
		t.Errorf("metric 2 error: got %q, want exit status 1", loaded.Metrics[2].ToolError)
	}
}

func TestSaveLoad_WithMetrics(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	ses.Metrics = []metrics.MetricEvent{
		{Timestamp: time.Now().Truncate(time.Second), TurnIndex: 1, Type: "llm", TTFT: 100 * time.Millisecond, Duration: time.Second},
		{Timestamp: time.Now().Truncate(time.Second), TurnIndex: 1, Type: "tool", ToolName: "glob", ToolSuccess: true, ToolDuration: 20 * time.Millisecond},
	}

	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(loaded.Metrics))
	}
	if loaded.Metrics[0].Type != "llm" || loaded.Metrics[0].TTFT != 100*time.Millisecond {
		t.Errorf("unexpected metric 0: %+v", loaded.Metrics[0])
	}
	if loaded.Metrics[1].Type != "tool" || loaded.Metrics[1].ToolName != "glob" {
		t.Errorf("unexpected metric 1: %+v", loaded.Metrics[1])
	}
}

func TestLastTurnIndex(t *testing.T) {
	ses := &Session{
		UsageHistory: []UsageEntry{
			{TurnIndex: 2},
			{TurnIndex: 4},
		},
		Metrics: []metrics.MetricEvent{
			{TurnIndex: 3},
			{TurnIndex: 5},
		},
	}
	if got := LastTurnIndex(ses); got != 5 {
		t.Fatalf("expected last turn index 5, got %d", got)
	}
	if got := LastTurnIndex(nil); got != 0 {
		t.Fatalf("expected nil session turn index 0, got %d", got)
	}
}
