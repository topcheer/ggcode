package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

func saveFullForTest(t *testing.T, store *JSONLStore, ses *Session) {
	t.Helper()
	_ = store.Save(ses)
	_ = store.AppendMetaToDisk(ses)
	if len(ses.Messages) > 0 {
		_ = store.AppendMessagesBatchToDisk(ses, ses.Messages)
	}
	for _, entry := range ses.UsageHistory {
		_ = store.AppendUsageEntry(ses, entry)
	}
	for _, ev := range ses.Metrics {
		_ = store.AppendMetric(ses, ev)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met before timeout")
	}
}

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

	saveFullForTest(t, store, ses)

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
	saveFullForTest(t, store, ses1)

	time.Sleep(1100 * time.Millisecond)
	ses2 := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses2.Title = "Second"
	ses2.Workspace = "/tmp/workspace-b"
	ses2.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}}}
	saveFullForTest(t, store, ses2)

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
	saveFullForTest(t, store, ses)

	msg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Follow up"}}}
	if err := store.AppendMessage(ses, msg); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := store.Load(ses.ID)
	if len(reloaded.Messages) != 1 {
		t.Fatalf("after append, messages=%d", len(reloaded.Messages))
	}
}

func TestAppendMessageGeneratesUTF8SafeTitle(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = ""
	saveFullForTest(t, store, ses)

	text := "这是一个很长很长的中文标题这是一个很长很长的中文标题这是一个很长很长的中文标题这是一个很长很长的中文标题"
	msg := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
	if err := store.AppendMessage(ses, msg); err != nil {
		t.Fatal(err)
	}

	if !utf8.ValidString(ses.Title) {
		t.Fatalf("expected valid UTF-8 title, got %q", ses.Title)
	}
	if got, want := ses.Title, util.Truncate(text, 60); got != want {
		t.Fatalf("title mismatch: got %q want %q", got, want)
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

	saveFullForTest(t, store, ses)

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.TunnelEvents) != 0 {
		t.Fatalf("expected 0 tunnel events (not persisted to JSONL), got %d", len(loaded.TunnelEvents))
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
	saveFullForTest(t, store, ses)

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
	saveFullForTest(t, store, ses)

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
	saveFullForTest(t, store, ses)

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

	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	}
	saveFullForTest(t, store, ses)

	compacted := []provider.Message{
		{ID: "msg_summary_1", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]\nUser asked about X"}}},
		{ID: "msg_followup_1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "follow-up"}}},
	}

	store.AppendMessageToDisk(ses, compacted[0])
	store.AppendMessageToDisk(ses, compacted[1])
	err := store.AppendCheckpoint(ses, compacted[0].ID, "", 50)
	if err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}

	store.AppendMessage(ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "response after checkpoint"}}})
	store.AppendMessage(ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "new question"}}})

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.Messages) != 6 {
		t.Fatalf("expected 6 Messages (all records), got %d", len(loaded.Messages))
	}

	if len(loaded.ContextMessages) != 4 {
		t.Fatalf("expected 4 ContextMessages (2 checkpoint + 2 post-checkpoint), got %d", len(loaded.ContextMessages))
	}

	if loaded.ContextMessages[0].Role != "system" {
		t.Fatalf("expected first ContextMessages role 'system', got '%s'", loaded.ContextMessages[0].Role)
	}

	lastCtx := loaded.ContextMessages[len(loaded.ContextMessages)-1]
	if lastCtx.Role != "user" {
		t.Fatalf("expected last ContextMessages role 'user', got '%s'", lastCtx.Role)
	}
	if lastCtx.Content[0].Text != "new question" {
		t.Fatalf("expected last text 'new question', got '%s'", lastCtx.Content[0].Text)
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
	saveFullForTest(t, store, ses)

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

	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "original msg"}}},
	}
	saveFullForTest(t, store, ses)

	cp1Summary := provider.Message{ID: "msg_cp1_summary", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary v1"}}}
	store.AppendMessageToDisk(ses, cp1Summary)
	store.AppendCheckpoint(ses, cp1Summary.ID, "", 100)

	store.AppendMessage(ses, provider.Message{ID: "msg_after_cp1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg after cp1"}}})

	cp2Summary := provider.Message{ID: "msg_cp2_summary", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary v2"}}}
	cp2Retained := provider.Message{ID: "msg_cp2_retained", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "retained context"}}}
	store.AppendMessageToDisk(ses, cp2Summary)
	store.AppendMessageToDisk(ses, cp2Retained)
	store.AppendCheckpoint(ses, cp2Summary.ID, "", 80)

	store.AppendMessage(ses, provider.Message{ID: "msg_final", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "final response"}}})

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.ContextMessages) != 3 {
		t.Fatalf("expected 3 ContextMessages (cp2Summary + cp2Retained + final), got %d", len(loaded.ContextMessages))
	}

	if loaded.ContextMessages[0].Content[0].Text != "summary v2" {
		t.Fatalf("expected first ContextMessages 'summary v2', got '%s'", loaded.ContextMessages[0].Content[0].Text)
	}

	if loaded.ContextMessages[2].Content[0].Text != "final response" {
		t.Fatalf("expected last ContextMessages 'final response', got '%s'", loaded.ContextMessages[2].Content[0].Text)
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
	saveFullForTest(t, store, ses)

	err := store.AppendCheckpoint(ses, "", "", 0)
	if err != nil {
		t.Fatalf("AppendCheckpoint with empty summary_msg_id failed: %v", err)
	}

	store.AppendMessage(ses, provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "after empty cp"}}})

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.ContextMessages) != 2 {
		t.Fatalf("expected 2 ContextMessages (no checkpoint fallback), got %d", len(loaded.ContextMessages))
	}

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
	saveFullForTest(t, store, ses)

	summaryMsg := provider.Message{ID: "msg_cp_summary_only", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary only"}}}
	retainedMsg := provider.Message{ID: "msg_cp_retained", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "retained user context"}}}
	store.AppendMessageToDisk(ses, summaryMsg)
	store.AppendMessageToDisk(ses, retainedMsg)
	store.AppendCheckpoint(ses, summaryMsg.ID, "", 10)

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.ContextMessages) != 2 {
		t.Fatalf("expected 2 ContextMessages from checkpoint, got %d", len(loaded.ContextMessages))
	}
	if loaded.ContextMessages[0].Role != "system" {
		t.Fatalf("expected first ContextMessages role 'system', got '%s'", loaded.ContextMessages[0].Role)
	}
	if loaded.ContextMessages[0].Content[0].Text != "summary only" {
		t.Fatalf("expected first text 'summary only', got '%s'", loaded.ContextMessages[0].Content[0].Text)
	}
	if loaded.ContextMessages[1].Role != "user" {
		t.Fatalf("expected second ContextMessages role 'user', got '%s'", loaded.ContextMessages[1].Role)
	}
	if loaded.ContextMessages[1].Content[0].Text != "retained user context" {
		t.Fatalf("expected second text 'retained user context', got '%s'", loaded.ContextMessages[1].Content[0].Text)
	}

	if len(loaded.Messages) != 3 {
		t.Fatalf("expected 3 message records (1 original + 2 checkpoint), got %d", len(loaded.Messages))
	}

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
	saveFullForTest(t, store, ses)

	originalUpdatedAt := ses.UpdatedAt
	time.Sleep(10 * time.Millisecond)

	compacted := []provider.Message{
		{ID: "msg_sum_5", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summarized"}}},
		{ID: "msg_ret_5", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post-summary"}}},
	}
	store.AppendMessageToDisk(ses, compacted[0])
	store.AppendMessageToDisk(ses, compacted[1])
	if err := store.AppendCheckpoint(ses, compacted[0].ID, "", 10); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}

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

	if found.Title != "index-test" {
		t.Fatalf("title lost in index: got '%s'", found.Title)
	}

	loaded, err := store.Load(found.ID)
	if err != nil {
		t.Fatalf("Load after index update failed: %v", err)
	}
	if len(loaded.ContextMessages) != 2 {
		t.Fatalf("expected 2 ContextMessages on reload, got %d", len(loaded.ContextMessages))
	}
}

func TestAppendCheckpointFileNotExist(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "no-file"

	compacted := []provider.Message{
		{ID: "msg_sum_6", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "from scratch"}}},
	}
	store.AppendMessageToDisk(ses, compacted[0])
	if err := store.AppendCheckpoint(ses, compacted[0].ID, "", 5); err != nil {
		t.Fatalf("AppendCheckpoint on non-existent file should succeed, got: %v", err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded.ContextMessages) != 1 {
		t.Fatalf("expected 1 ContextMessages, got %d", len(loaded.ContextMessages))
	}
	if loaded.ContextMessages[0].Content[0].Text != "from scratch" {
		t.Fatalf("expected 'from scratch', got '%s'", loaded.ContextMessages[0].Content[0].Text)
	}
}

func TestSaveCreatesEmptySessionFile(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "default", "model")

	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("session file should exist after Save: %v", err)
	}

	if err := store.Delete(ses.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("empty session file should be deleted")
	}
}

func TestDeleteCleansUpEmptySession(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)

	ses := NewSession("zai", "default", "model")
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}}
	saveFullForTest(t, store, ses)
	path := filepath.Join(dir, ses.ID+".jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("file should exist after save with messages")
	}

	if err := store.Delete(ses.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestHasUserInteraction(t *testing.T) {
	ses := NewSession("zai", "default", "model")
	if ses.HasUserInteraction() {
		t.Error("new session should have no user interaction")
	}

	ses.Messages = []provider.Message{{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if ses.HasUserInteraction() {
		t.Error("assistant-only session should have no user interaction")
	}

	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if !ses.HasUserInteraction() {
		t.Error("session with user text should have user interaction")
	}

	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "  "}}}}
	if ses.HasUserInteraction() {
		t.Error("session with whitespace-only user text should have no user interaction")
	}

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
	saveFullForTest(t, store, ses)

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

	ses1 := NewSession("zai", "default", "model")
	ses1.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "keep me"}}}}
	saveFullForTest(t, store, ses1)

	ses2 := NewSession("zai", "default", "model")

	metaPath := filepath.Join(dir, ses2.ID+".jsonl")
	f, _ := os.Create(metaPath)
	json.NewEncoder(f).Encode(jsonlRecord{Type: "meta", SessionID: ses2.ID, Title: "empty"})
	f.Close()

	idx, _ := store.loadIndex()
	idx = append(idx, indexEntry{ID: ses2.ID, Title: "empty"})
	store.saveIndex(idx)

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("first List should return index entries before background cleanup, got %d", len(list))
	}

	waitForCondition(t, 2*time.Second, func() bool {
		if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
			return false
		}
		list, err := store.List()
		if err != nil {
			return false
		}
		return len(list) == 1 && list[0].ID == ses1.ID
	})
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
	saveFullForTest(t, store, ses)

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

	if loaded.UsageHistory[0].TurnIndex != 1 {
		t.Errorf("entry 0 turn index: got %d, want 1", loaded.UsageHistory[0].TurnIndex)
	}
	if loaded.UsageHistory[0].Usage.InputTokens != 500 {
		t.Errorf("entry 0 input tokens: got %d, want 500", loaded.UsageHistory[0].Usage.InputTokens)
	}
	if loaded.UsageHistory[0].Usage.CacheRead != 200 {
		t.Errorf("entry 0 cache read: got %d, want 200", loaded.UsageHistory[0].Usage.CacheRead)
	}

	if loaded.UsageHistory[2].TurnIndex != 2 {
		t.Errorf("entry 2 turn index: got %d, want 2", loaded.UsageHistory[2].TurnIndex)
	}
	if loaded.UsageHistory[2].Usage.InputTokens != 600 {
		t.Errorf("entry 2 input tokens: got %d, want 600", loaded.UsageHistory[2].Usage.InputTokens)
	}

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
	saveFullForTest(t, store, ses)

	entry1 := UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: 1,
		Usage:     provider.TokenUsage{InputTokens: 500, OutputTokens: 100},
	}
	if err := store.AppendUsageEntry(ses, entry1); err != nil {
		t.Fatal(err)
	}

	checkpointMsgs := []provider.Message{
		{ID: "msg_cp_hello", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}}},
		{ID: "msg_cp_hi", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "Hi!"}}},
	}
	store.AppendMessageToDisk(ses, checkpointMsgs[0])
	store.AppendMessageToDisk(ses, checkpointMsgs[1])
	if err := store.AppendCheckpoint(ses, checkpointMsgs[0].ID, "", 50); err != nil {
		t.Fatal(err)
	}

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

	if len(loaded.UsageHistory) != 2 {
		t.Fatalf("expected 2 usage entries (all preserved across checkpoint), got %d", len(loaded.UsageHistory))
	}
	if loaded.UsageHistory[0].TurnIndex != 1 {
		t.Errorf("entry 0 turn index: got %d, want 1", loaded.UsageHistory[0].TurnIndex)
	}
	if loaded.UsageHistory[1].TurnIndex != 2 {
		t.Errorf("entry 1 turn index: got %d, want 2", loaded.UsageHistory[1].TurnIndex)
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

	saveFullForTest(t, store, ses)

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
	saveFullForTest(t, store, ses)

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

	if loaded.Metrics[1].Type != "tool" {
		t.Errorf("metric 1 type: got %q, want tool", loaded.Metrics[1].Type)
	}
	if loaded.Metrics[1].ToolName != "read_file" {
		t.Errorf("metric 1 tool: got %q, want read_file", loaded.Metrics[1].ToolName)
	}
	if !loaded.Metrics[1].ToolSuccess {
		t.Error("metric 1 should be success")
	}

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

	saveFullForTest(t, store, ses)

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

func TestAppendMessagesBatchToDisk(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	saveFullForTest(t, store, ses)

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg1"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "reply1"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg2"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "reply2"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "msg3"}}},
	}
	if err := store.AppendMessagesBatchToDisk(ses, msgs); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := store.Load(ses.ID)
	if len(reloaded.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(reloaded.Messages))
	}
	for i, want := range []string{"msg1", "reply1", "msg2", "reply2", "msg3"} {
		got := reloaded.Messages[i].Content[0].Text
		if got != want {
			t.Errorf("message %d: expected %q, got %q", i, want, got)
		}
	}
}

func TestAppendMessagesBatchToDiskEmpty(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	saveFullForTest(t, store, ses)

	if err := store.AppendMessagesBatchToDisk(ses, nil); err != nil {
		t.Fatalf("expected nil error for empty batch, got %v", err)
	}

	reloaded, _ := store.Load(ses.ID)
	if reloaded != nil && len(reloaded.Messages) != 0 {
		t.Fatalf("expected 0 messages after empty batch, got %d", len(reloaded.Messages))
	}
}

func TestAppendMessagesBatchToDiskMixedWithSingle(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	saveFullForTest(t, store, ses)

	store.AppendMessageToDisk(ses, provider.Message{
		Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "single"}},
	})

	store.AppendMessagesBatchToDisk(ses, []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "batch1"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "batch2"}}},
	})

	store.AppendMessageToDisk(ses, provider.Message{
		Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "single2"}},
	})

	reloaded, _ := store.Load(ses.ID)
	if len(reloaded.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(reloaded.Messages))
	}
	want := []string{"single", "batch1", "batch2", "single2"}
	for i, w := range want {
		if reloaded.Messages[i].Content[0].Text != w {
			t.Errorf("message %d: expected %q, got %q", i, w, reloaded.Messages[i].Content[0].Text)
		}
	}
}

func TestMigrateMessageIDs(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_migrate_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "ep", "model")
	ses.Title = "Migration Test"
	ses.Workspace = "/test"

	path := filepath.Join(dir, ses.ID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	enc.Encode(jsonlRecord{Type: "meta", SessionID: ses.ID, Title: ses.Title, Workspace: ses.Workspace})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}})
	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi there"}}}})
	f.Close()

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Messages))
	}

	if len(loaded.ContextMessages) != 2 {
		t.Fatalf("expected 2 context messages, got %d", len(loaded.ContextMessages))
	}

	loaded2, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded2.Messages) != len(loaded.Messages) {
		t.Fatalf("second load: message count mismatch %d vs %d", len(loaded2.Messages), len(loaded.Messages))
	}

	for i := range loaded.Messages {
		if loaded.Messages[i].ID != loaded2.Messages[i].ID {
			t.Fatalf("message %d ID changed across loads: %s vs %s", i, loaded.Messages[i].ID, loaded2.Messages[i].ID)
		}
	}

	if _, err := os.Stat(path + ".migrate.tmp"); !os.IsNotExist(err) {
		t.Fatal("migration temp file left behind")
	}
}

func TestMigrateOldCheckpoint(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_cp_migrate_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "ep", "model")
	ses.Title = "Checkpoint Migration"
	ses.Workspace = "/test"

	path := filepath.Join(dir, ses.ID+".jsonl")
	f, _ := os.Create(path)
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	enc.Encode(jsonlRecord{Type: "meta", SessionID: ses.ID, Title: ses.Title, Workspace: ses.Workspace})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old msg 1"}}}})
	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "old msg 2"}}}})

	summaryMsg := provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "[Previous conversation summary]\nWe discussed testing.",
		}},
	}
	enc.Encode(jsonlRecord{
		Type:               "checkpoint",
		CheckpointMessages: []provider.Message{summaryMsg},
		CheckpointTokens:   5000,
	})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post checkpoint"}}}})
	f.Close()

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	postCpMsgs := loaded.Messages[3:]
	for i, msg := range postCpMsgs {
		if msg.ID == "" {
			t.Fatalf("post-checkpoint message %d has empty ID after migration", i)
		}
	}

	if len(loaded.ContextMessages) < 2 {
		t.Fatalf("expected at least 2 context messages, got %d", len(loaded.ContextMessages))
	}

	firstMsg := loaded.ContextMessages[0]
	if firstMsg.Role != "system" {
		t.Fatalf("expected first context message to be system role, got %s", firstMsg.Role)
	}
	if !strings.Contains(firstMsg.Content[0].Text, "[Previous conversation summary]") {
		t.Fatalf("expected first context message to be summary")
	}

	diskF, _ := os.Open(path)
	sc := bufio.NewScanner(diskF)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	foundNewCheckpoint := false
	foundOldCheckpoint := false
	for sc.Scan() {
		var rec jsonlRecord
		if json.Unmarshal([]byte(sc.Text()), &rec) != nil {
			continue
		}
		if rec.Type == "checkpoint" {
			if rec.CheckpointSummaryMsgID != "" {
				foundNewCheckpoint = true
			}
			if len(rec.CheckpointMessages) > 0 {
				foundOldCheckpoint = true
			}
		}
	}
	diskF.Close()

	if !foundNewCheckpoint {
		t.Fatal("new checkpoint format (checkpoint_summary_msg_id) not found after migration")
	}
	if foundOldCheckpoint {
		t.Fatal("old checkpoint format (checkpoint_messages) still present after migration")
	}
}

func TestMigrateSkipWhenLastCheckpointIsNewFormat(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_skip_migrate_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "ep", "model")
	ses.Title = "Skip Migration"
	ses.Workspace = "/test"

	path := filepath.Join(dir, ses.ID+".jsonl")
	f, _ := os.Create(path)
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	enc.Encode(jsonlRecord{Type: "meta", SessionID: ses.ID, Title: ses.Title, Workspace: ses.Workspace})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old msg"}}}})

	summaryMsg := provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "[Previous conversation summary]\nOld summary.",
		}},
	}
	enc.Encode(jsonlRecord{
		Type:               "checkpoint",
		CheckpointMessages: []provider.Message{summaryMsg},
		CheckpointTokens:   1000,
	})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{ID: "msg_after_old_cp", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "after old cp"}}}})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{ID: "msg_new_summary", Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]\nNew summary."}}}})

	enc.Encode(jsonlRecord{
		Type:                   "checkpoint",
		CheckpointSummaryMsgID: "msg_new_summary",
		CheckpointLastMsgID:    "msg_after_old_cp",
		CheckpointTokens:       5000,
	})

	enc.Encode(jsonlRecord{Type: "message", Message: &provider.Message{ID: "msg_post_new", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "after new cp"}}}})
	f.Close()

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}

	diskF, _ := os.Open(path)
	sc := bufio.NewScanner(diskF)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	oldCpCount := 0
	newCpCount := 0
	oldCpHasMessages := false
	for sc.Scan() {
		var rec jsonlRecord
		if json.Unmarshal([]byte(sc.Text()), &rec) != nil {
			continue
		}
		if rec.Type == "checkpoint" {
			if rec.CheckpointSummaryMsgID != "" {
				newCpCount++
			}
			if len(rec.CheckpointMessages) > 0 {
				oldCpCount++
				oldCpHasMessages = true
			}
		}
	}
	diskF.Close()

	if oldCpCount != 1 {
		t.Fatalf("expected 1 old-format checkpoint on disk, got %d", oldCpCount)
	}
	if !oldCpHasMessages {
		t.Fatal("old checkpoint should still have checkpoint_messages (migration should have been skipped)")
	}

	if newCpCount != 1 {
		t.Fatalf("expected 1 new-format checkpoint on disk, got %d", newCpCount)
	}

	if len(loaded.ContextMessages) != 2 {
		t.Fatalf("expected 2 context messages (summary + 1 post-checkpoint), got %d", len(loaded.ContextMessages))
	}

	if loaded.ContextMessages[0].ID != "msg_new_summary" {
		t.Fatalf("expected first context message to be msg_new_summary, got %s", loaded.ContextMessages[0].ID)
	}

	if loaded.ContextMessages[1].ID != "msg_post_new" {
		t.Fatalf("expected second context message to be msg_post_new, got %s", loaded.ContextMessages[1].ID)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestLoadCheckpointWithMissingLastMsgID(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_test_*")
	defer os.RemoveAll(dir)

	store, _ := NewJSONLStore(dir)
	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "missing-lastmsgid"
	ses.Workspace = "/tmp/test-ws"

	ses.Messages = []provider.Message{
		{ID: "msg_1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old question"}}},
		{ID: "msg_2", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "old answer"}}},
	}
	saveFullForTest(t, store, ses)

	summaryMsg := provider.Message{
		ID:   "msg_summary",
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: "[Previous conversation summary]\nUser discussed old topics.",
		}},
	}
	postMsg1 := provider.Message{ID: "msg_post_1", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "post-checkpoint question"}}}
	postMsg2 := provider.Message{ID: "msg_post_2", Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "post-checkpoint answer"}}}

	store.AppendMessageToDisk(ses, summaryMsg)
	store.AppendMessageToDisk(ses, postMsg1)
	store.AppendMessageToDisk(ses, postMsg2)

	store.AppendCheckpoint(ses, summaryMsg.ID, "msg_NONEXISTENT", 42)

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(loaded.ContextMessages) != 3 {
		t.Fatalf("expected 3 ContextMessages (summary + 2 post-checkpoint via fallback), got %d", len(loaded.ContextMessages))
	}

	if loaded.ContextMessages[0].ID != "msg_summary" {
		t.Fatalf("expected first ContextMessage 'msg_summary', got '%s'", loaded.ContextMessages[0].ID)
	}

	if loaded.ContextMessages[1].ID != "msg_post_1" {
		t.Fatalf("expected second ContextMessage 'msg_post_1', got '%s'", loaded.ContextMessages[1].ID)
	}

	if loaded.ContextMessages[2].ID != "msg_post_2" {
		t.Fatalf("expected third ContextMessage 'msg_post_2', got '%s'", loaded.ContextMessages[2].ID)
	}
}

func TestSessionPathSanitization(t *testing.T) {
	s, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	evil := []string{"../../etc/passwd", "..\\..\\x", "a/b", "a/b/c", "-", ""}
	for _, id := range evil {
		p := s.sessionPath(id)
		if filepath.Dir(p) != s.dir {
			t.Errorf("id %q escaped store dir: %q (dir=%q)", id, p, s.dir)
		}
		if strings.Contains(p, "passwd") || strings.Contains(p, "..") {
			t.Errorf("id %q leaked into path: %q", id, p)
		}
	}

	good := filepath.Join(s.dir, "abc123-_X.jsonl")
	if got := s.sessionPath("abc123-_X"); got != good {
		t.Errorf("valid id resolved to %q, want %q", got, good)
	}
}

func TestMigrateMessageIDsSkipsWhenLockHeld(t *testing.T) {
	dir, _ := os.MkdirTemp("", "ggcode_migrate_lock_*")
	defer os.RemoveAll(dir)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "ep", "model")
	path := filepath.Join(dir, ses.ID+".jsonl")
	content := []byte("{\"type\":\"meta\",\"session_id\":\"" + ses.ID + "\",\"title\":\"t\"}\n" +
		"{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"hello\"}]}}\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(path+".flock", 0o755); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(path)
	migrated, err := store.migrateMessageIDs(ses.ID)

	if err != nil {
		t.Fatalf("migration under foreign lock must skip without error, got %v", err)
	}
	if migrated != 0 {
		t.Fatalf("expected 0 migrations when lock held, got %d", migrated)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("session file must be untouched when migration is skipped")
	}
}
