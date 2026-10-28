package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue4591_LegacyCheckpointClearsStaleSummaryMsgID pins #4591: when a
// legacy snapshot checkpoint appears AFTER a new-format checkpoint and the
// new-format summary message line is flushed LATE (after the legacy line),
// the loader must honor the newest (legacy) snapshot - not resurrect the
// stale new-format summary via lastCpSummaryMsgID.
//
// File order (byte order in JSONL):
//  1. meta
//  2. new-format checkpoint cp1 (summary id "sum-cp1", last msg "early1")
//  3. message "early1" (pre-cp2 traffic, exists only for cp1 era)
//  4. legacy checkpoint cp2 whose snapshot contains "legacy-newer"
//  5. message "sum-cp1" - the cp1 summary, flushed AFTER the legacy line
//  6. message "post1" - post-cp2 traffic
//
// Expected: context = cp2 snapshot ("legacy-newer") + messages after cp2.
// Buggy behavior (before fix): summary branch wins with stale cp1 summary,
// dropping everything covered by the newer cp2 snapshot.
func TestIssue4591_LegacyCheckpointClearsStaleSummaryMsgID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := "late-flush-4591"
	path := filepath.Join(dir, id+".jsonl")
	now := time.Now()

	writeJSONLLine(t, path, jsonlRecord{Type: "meta", SessionID: id, Title: "t", Workspace: dir})

	// 1. New-format checkpoint cp1.
	writeJSONLLine(t, path, jsonlRecord{
		Type:                   "checkpoint",
		SessionID:              id,
		CheckpointSummaryMsgID: "sum-cp1",
		CheckpointLastMsgID:    "early1",
		CheckpointTokens:       100,
	})

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

	// 2. cp1-era traffic (covered by cp1's summary).
	writeJSONLLine(t, path, mk("early1", "early traffic", now.Add(time.Second)))

	// 3. Legacy checkpoint cp2 - the NEWEST checkpoint, supersedes cp1.
	writeJSONLLine(t, path, jsonlRecord{
		Type:      "checkpoint",
		SessionID: id,
		CheckpointMessages: []provider.Message{{
			ID:      "legacy-newer",
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: "newer legacy snapshot content"}},
		}},
		CheckpointTokens: 200,
	})

	// 4. cp1's summary line arrives LATE - after the legacy checkpoint line.
	writeJSONLLine(t, path, mk("sum-cp1", "stale cp1 summary content", now.Add(2*time.Second)))

	// 5. Post-cp2 traffic.
	writeJSONLLine(t, path, mk("post1", "after cp2", now.Add(3*time.Second)))

	ses, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := make(map[string]bool)
	for _, m := range ses.ContextMessages {
		got[m.ID] = true
	}
	if !got["legacy-newer"] {
		t.Fatalf("newest (legacy cp2) snapshot must provide the context base, got %v", got)
	}
	if len(ses.ContextMessages) == 0 || ses.ContextMessages[0].ID != "legacy-newer" {
		t.Fatalf("cp2 snapshot must be the context base, first message = %v", ses.ContextMessages)
	}
	if !got["post1"] {
		t.Fatalf("post-cp2 message must be present after the snapshot, got %v", got)
	}
}
