package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestLoadSession_NoCheckpointContextUnwindowed pins the fix for the
// "old session reloads with only a few K left" bug: for checkpoint-less
// sessions, ContextMessages must be built from the UNWINDOWED message list
// (capped at MaxContextMessages), never from the 24h render-windowed
// ses.Messages slice.
func TestLoadSession_NoCheckpointContextUnwindowed(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_ctxwin_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "ctx-unwindowed"
	path := filepath.Join(dir, id+".jsonl")

	writeJSONLLine(t, path, jsonlRecord{Type: "meta", SessionID: id, Title: "t", Workspace: dir})

	// 600 messages, all 48h old: above recentMessageThreshold (500) and
	// outside the 24h render window. No checkpoint anywhere.
	stale := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 600; i++ {
		writeJSONLLine(t, path, jsonlRecord{
			Type: "message",
			Message: &provider.Message{
				ID:      fmt.Sprintf("m-%d", i),
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("body %d", i)}},
			},
			Timestamp: stale.Add(time.Duration(i) * time.Millisecond),
		})
	}

	ses, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Render window may legitimately keep few/none of these messages.
	// The agent context, however, must carry the unwindowed tail: the
	// 200-message cap plus the truncation note.
	want := MaxContextMessages + 1
	if len(ses.ContextMessages) != want {
		t.Fatalf("ContextMessages = %d, want %d (cap %d + truncation note); render window held %d messages",
			len(ses.ContextMessages), want, MaxContextMessages, len(ses.Messages))
	}
	last := ses.ContextMessages[len(ses.ContextMessages)-1]
	if !strings.Contains(last.Content[0].Text, "body 599") {
		t.Fatalf("context tail = %q, want the true last disk message (body 599)", last.Content[0].Text)
	}
	// The window must include messages far older than the 24h render window.
	foundOld := false
	for _, m := range ses.ContextMessages {
		if len(m.Content) > 0 && strings.HasPrefix(m.Content[0].Text, "body 4") {
			foundOld = true
			break
		}
	}
	if !foundOld {
		t.Fatal("context contains no messages from the 4xx range -- unwindowed tail not restored")
	}
	// The truncation note must be present so the agent knows history was cut.
	if !strings.Contains(ses.ContextMessages[0].Content[0].Text, "truncated") {
		t.Fatalf("context head = %q, want truncation note", ses.ContextMessages[0].Content[0].Text)
	}
}

// TestLoadSession_SummaryAfterLastMsgID pins the async-precompact ordering
// fix: when the summary message is persisted AFTER last_msg_id in the file,
// the extra-message search must still find last_msg_id and restore the
// messages between it and the summary (the TOCTOU extras kept verbatim after
// compaction). The old summary-anchored search missed it and fell back to
// "everything after the summary", dropping those messages.
func TestLoadSession_SummaryAfterLastMsgID(t *testing.T) {
	dir, err := os.MkdirTemp("", "ggcode_sumord_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "summary-after-last"
	path := filepath.Join(dir, id+".jsonl")

	writeJSONLLine(t, path, jsonlRecord{Type: "meta", SessionID: id, Title: "t", Workspace: dir})
	now := time.Now()
	msg := func(id_, text string, ts time.Time) jsonlRecord {
		return jsonlRecord{
			Type: "message",
			Message: &provider.Message{
				ID:      id_,
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: text}},
			},
			Timestamp: ts,
		}
	}

	// File order (async precompact): pre-compaction messages, last_msg_id,
	// TOCTOU extra written while the LLM summarized, THEN the summary
	// message, then post-compaction traffic, then the checkpoint record.
	writeJSONLLine(t, path, msg("a1", "pre-compaction one", now))
	writeJSONLLine(t, path, msg("e1", "pre-compaction two", now.Add(time.Second)))
	writeJSONLLine(t, path, msg("lm", "last message at compaction time", now.Add(2*time.Second)))
	writeJSONLLine(t, path, msg("e2", "TOCTOU extra during async precompact", now.Add(3*time.Second)))
	writeJSONLLine(t, path, jsonlRecord{
		Type: "message",
		Message: &provider.Message{
			ID:   "sum",
			Role: "system",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "[Previous conversation summary] everything before the checkpoint was summarized here.",
			}},
		},
		Timestamp: now.Add(4 * time.Second),
	})
	writeJSONLLine(t, path, msg("post", "post-compaction message", now.Add(5*time.Second)))
	writeJSONLLine(t, path, jsonlRecord{
		Type:                   "checkpoint",
		SessionID:              id,
		CheckpointSummaryMsgID: "sum",
		CheckpointLastMsgID:    "lm",
		CheckpointTokens:       1234,
	})

	ses, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := make(map[string]bool)
	for _, m := range ses.ContextMessages {
		got[m.ID] = true
	}
	if !got["sum"] {
		t.Fatal("summary message missing from context")
	}
	if !got["e2"] {
		t.Fatal("TOCTOU extra (between last_msg_id and summary) was dropped -- summary-anchored search regression")
	}
	if !got["post"] {
		t.Fatal("post-compaction message missing from context")
	}
	if got["a1"] || got["e1"] {
		t.Fatal("pre-compaction messages leaked into context (they are folded into the summary)")
	}
}
