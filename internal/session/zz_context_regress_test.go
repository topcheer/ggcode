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

	want := MaxContextMessages + 1
	if len(ses.ContextMessages) != want {
		t.Fatalf("ContextMessages = %d, want %d (cap %d + truncation note); render window held %d messages",
			len(ses.ContextMessages), want, MaxContextMessages, len(ses.Messages))
	}
	last := ses.ContextMessages[len(ses.ContextMessages)-1]
	if !strings.Contains(last.Content[0].Text, "body 599") {
		t.Fatalf("context tail = %q, want the true last disk message (body 599)", last.Content[0].Text)
	}

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

	if !strings.Contains(ses.ContextMessages[0].Content[0].Text, "truncated") {
		t.Fatalf("context head = %q, want truncation note", ses.ContextMessages[0].Content[0].Text)
	}
}

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
