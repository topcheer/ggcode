package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue4536_NewFormatCheckpointSupersedesLegacy pins #4536: in a mixed
// session (legacy snapshot checkpoint first, new-format checkpoint later),
// a missing/corrupted new-format summary message must fall through to the
// no-checkpoint recent-messages path - NOT silently roll back to the stale
// legacy snapshot.
func TestIssue4536_NewFormatCheckpointSupersedesLegacy(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "mixed-cp-4536"
	path := filepath.Join(dir, id+".jsonl")
	now := time.Now()

	writeJSONLLine(t, path, jsonlRecord{Type: "meta", SessionID: id, Title: "t", Workspace: dir})

	// Legacy checkpoint whose snapshot contains a message that exists ONLY
	// inside CheckpointMessages (never as a JSONL message line).
	writeJSONLLine(t, path, jsonlRecord{
		Type:      "checkpoint",
		SessionID: id,
		CheckpointMessages: []provider.Message{{
			ID:      "legacy-only",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "stale legacy snapshot content"}},
		}},
		CheckpointTokens: 100,
	})

	// Post-checkpoint traffic that is NOT inside the legacy snapshot.
	mk := func(id_, text string, ts time.Time) jsonlRecord {
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
	writeJSONLLine(t, path, mk("post1", "recent one", now.Add(time.Second)))
	writeJSONLLine(t, path, mk("post2", "recent two", now.Add(2*time.Second)))

	// New-format checkpoint whose summary message ID does not exist in the
	// JSONL (corrupted/trimmed summary).
	writeJSONLLine(t, path, jsonlRecord{
		Type:                   "checkpoint",
		SessionID:              id,
		CheckpointSummaryMsgID: "sum-missing",
		CheckpointLastMsgID:    "post2",
		CheckpointTokens:       200,
	})

	ses, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := make(map[string]bool)
	for _, m := range ses.ContextMessages {
		got[m.ID] = true
	}
	if got["legacy-only"] {
		t.Fatal("stale legacy snapshot leaked into context after a later new-format checkpoint lost its summary (#4536)")
	}
	if !got["post1"] || !got["post2"] {
		t.Fatalf("no-checkpoint recent path must serve the recent messages, got %v", got)
	}
}
