package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestDedupMessageRecords_RemovesExactDuplicates(t *testing.T) {
	msg := &provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "hello world"},
		},
	}
	records := []jsonlRecord{
		{Type: "message", Message: msg},
		{Type: "message", Message: msg},
		{Type: "message", Message: msg},
	}
	got := dedupMessageRecords(records)
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
}

func TestDedupMessageRecords_KeepsDistinctMessages(t *testing.T) {
	records := []jsonlRecord{
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "first"}}}},
		{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "second"}}}},
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "third"}}}},
	}
	got := dedupMessageRecords(records)
	if len(got) != 3 {
		t.Fatalf("expected 3 records, got %d", len(got))
	}
}

func TestDedupMessageRecords_PassesThroughNonMessages(t *testing.T) {
	records := []jsonlRecord{
		{Type: "checkpoint"},
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}},
		{Type: "usage"},
		{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}},
		{Type: "meta"},
	}
	got := dedupMessageRecords(records)
	// checkpoint, message(hi), usage, meta = 4 (second message(hi) removed)
	if len(got) != 4 {
		t.Fatalf("expected 4 records, got %d", len(got))
	}
}

func TestDedupMessageRecords_ToolUseFingerprint(t *testing.T) {
	msg1 := &provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "read_file", ToolID: "tool-1", Input: []byte(`{"path":"/tmp/a"}`)},
		},
	}
	msg2 := &provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "read_file", ToolID: "tool-1", Input: []byte(`{"path":"/tmp/a"}`)},
		},
	}
	msg3 := &provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolName: "read_file", ToolID: "tool-2", Input: []byte(`{"path":"/tmp/b"}`)},
		},
	}
	records := []jsonlRecord{
		{Type: "message", Message: msg1},
		{Type: "message", Message: msg2}, // duplicate of msg1
		{Type: "message", Message: msg3}, // distinct (different ToolID + Input)
	}
	got := dedupMessageRecords(records)
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
}

func TestDedupMessageRecords_ToolResultFingerprint(t *testing.T) {
	msg1 := &provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "tool-1", Output: "file contents here"},
		},
	}
	msg2 := &provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "tool-1", Output: "file contents here"},
		},
	}
	records := []jsonlRecord{
		{Type: "message", Message: msg1},
		{Type: "message", Message: msg2},
	}
	got := dedupMessageRecords(records)
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
}

func TestDedupLightweightEntries_MixedTypes(t *testing.T) {
	msg := &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "dup"}}}
	entries := []localLightweightEntry{
		{recType: "message", record: jsonlRecord{Type: "message", Message: msg}},
		{recType: "cost", record: jsonlRecord{Type: "cost"}},
		{recType: "message", record: jsonlRecord{Type: "message", Message: msg}}, // dup
		{recType: "cost", record: jsonlRecord{Type: "cost"}},                     // cost, not deduped
		{recType: "message", record: jsonlRecord{Type: "message", Message: &provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "unique"}}}}},
	}
	got := dedupLightweightEntries(entries)
	// Expected: message(dup), cost, cost, message(unique) = 4
	// The second message(dup) is removed
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(got))
	}
}

func TestDedupMessageRecords_SingleOrEmpty(t *testing.T) {
	if got := dedupMessageRecords(nil); len(got) != 0 {
		t.Fatal("nil input should return empty")
	}
	one := []jsonlRecord{{Type: "message", Message: &provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "only"}}}}}
	if got := dedupMessageRecords(one); len(got) != 1 {
		t.Fatalf("single record should pass through, got %d", len(got))
	}
}

// TestLoadSession_DeduplicatesCorruptedJSONL verifies that loadSession
// correctly deduplicates message records in a JSONL file corrupted by the
// runAdded duplication bug.
func TestLoadSession_DeduplicatesCorruptedJSONL(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Build a session with duplicate messages
	ses := &Session{
		ID:        "dedup-test",
		Title:     "Dedup Test",
		Workspace: dir,
	}
	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "hello"}},
	}
	ses.Messages = []provider.Message{msg}
	ses.ContextMessages = ses.Messages

	// Save once
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	// Append the same message multiple times to simulate corruption
	for i := 0; i < 5; i++ {
		if err := store.AppendMessageToDisk(ses, msg); err != nil {
			t.Fatal(err)
		}
	}

	// Load — should deduplicate
	loaded, err := store.Load("dedup-test")
	if err != nil {
		t.Fatal(err)
	}

	// Should have exactly 1 unique message (the original + 5 duplicates → 1)
	userMsgs := 0
	for _, m := range loaded.Messages {
		if m.Role == "user" {
			for _, c := range m.Content {
				if c.Type == "text" && c.Text == "hello" {
					userMsgs++
				}
			}
		}
	}
	if userMsgs != 1 {
		t.Fatalf("expected 1 unique 'hello' message after dedup, got %d", userMsgs)
	}
}
