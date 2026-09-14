package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// newDeltaStore builds a JSONLStore rooted in a temp dir.
func newDeltaStore(t *testing.T) (*session.JSONLStore, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := session.NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	return st, filepath.Join(dir, "sessions")
}

func textMsg(id, role, text string) provider.Message {
	return provider.Message{
		ID:   id,
		Role: role,
		Content: []provider.ContentBlock{
			{Type: "text", Text: text},
		},
	}
}

// jsonlMessageLines counts message records in the session's JSONL file.
func jsonlMessageLines(t *testing.T, dir, sesID string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, sesID+".jsonl"))
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" && strings.Contains(line, `"type":"message"`) {
			n++
		}
	}
	return n
}

// TestSaveSessionMessagesDeltaAppend verifies #1495 case B: saving the
// full message slice repeatedly must append only the new tail, not the
// entire history. Old behavior appended ses.Messages wholesale on every
// save, growing the JSONL quadratically and relying on Load-side dedup.
func TestSaveSessionMessagesDeltaAppend(t *testing.T) {
	st, dir := newDeltaStore(t)
	ses := &session.Session{ID: "delta-1", Title: "t"}

	first := []provider.Message{
		textMsg("msg_1", "user", "hello"),
		textMsg("msg_2", "assistant", "hi there"),
	}
	if err := SaveSessionMessages(st, ses, first); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if got := jsonlMessageLines(t, dir, ses.ID); got != 2 {
		t.Fatalf("after save 1: %d message records, want 2", got)
	}

	// Save 2: full history again plus one new message. Only the new
	// message may be appended (old code wrote all 3 again -> 5 records).
	full := append(append([]provider.Message{}, first...), textMsg("msg_3", "user", "more"))
	if err := SaveSessionMessages(st, ses, full); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if got := jsonlMessageLines(t, dir, ses.ID); got != 3 {
		t.Fatalf("after save 2: %d message records, want 3 (delta append)", got)
	}

	// Save 3: unchanged history -> zero new records.
	if err := SaveSessionMessages(st, ses, full); err != nil {
		t.Fatalf("save 3: %v", err)
	}
	if got := jsonlMessageLines(t, dir, ses.ID); got != 3 {
		t.Fatalf("after save 3 (no new): %d message records, want 3", got)
	}

	// Reload: full history intact, in order.
	loaded, err := st.Load(ses.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Messages) != 3 {
		t.Fatalf("loaded %d messages, want 3", len(loaded.Messages))
	}
	if firstTextOf(loaded.Messages[2]) != "more" {
		t.Fatalf("last message text = %q, want more", firstTextOf(loaded.Messages[2]))
	}
}

// TestSaveSessionMessagesLegacyNoIDs covers ID-less records (older
// ggcode versions): prefix matching falls back to role + first text.
func TestSaveSessionMessagesLegacyNoIDs(t *testing.T) {
	st, dir := newDeltaStore(t)
	ses := &session.Session{ID: "delta-2", Title: "t"}

	first := []provider.Message{textMsg("", "user", "q1")}
	if err := SaveSessionMessages(st, ses, first); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	full := []provider.Message{textMsg("", "user", "q1"), textMsg("", "assistant", "a1")}
	if err := SaveSessionMessages(st, ses, full); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if got := jsonlMessageLines(t, dir, ses.ID); got != 2 {
		t.Fatalf("legacy: %d message records, want 2", got)
	}
}

// TestSaveSessionMessagesHistoryRewrite covers a rewritten history (the
// new slice diverges from old): the delta is everything from the first
// mismatch, preserving old full-append behavior so Load-side dedup
// keeps the file correct.
func TestSaveSessionMessagesHistoryRewrite(t *testing.T) {
	st, dir := newDeltaStore(t)
	ses := &session.Session{ID: "delta-3", Title: "t"}

	if err := SaveSessionMessages(st, ses, []provider.Message{
		textMsg("msg_1", "user", "original"),
	}); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	// History rewritten: first message replaced (different ID).
	if err := SaveSessionMessages(st, ses, []provider.Message{
		textMsg("msg_9", "user", "rewritten"),
		textMsg("msg_10", "assistant", "answer"),
	}); err != nil {
		t.Fatalf("save 2: %v", err)
	}
	// Prefix match fails at position 0 -> full append of both records.
	if got := jsonlMessageLines(t, dir, ses.ID); got != 3 {
		t.Fatalf("rewrite: %d message records, want 3 (fallback full append)", got)
	}
	// Load-side dedup semantics unchanged: Load returns a usable session.
	if _, err := st.Load(ses.ID); err != nil {
		t.Fatalf("load after rewrite: %v", err)
	}
}

// TestPersistedPrefixLen is a table test for the prefix helper itself.
func TestPersistedPrefixLen(t *testing.T) {
	old := []provider.Message{
		textMsg("a", "user", "1"),
		textMsg("b", "assistant", "2"),
	}
	cases := []struct {
		name string
		msgs []provider.Message
		want int
	}{
		{"same", old, 2},
		{"extended", []provider.Message{old[0], old[1], textMsg("c", "user", "3")}, 2},
		{"diverge at 1", []provider.Message{old[0], textMsg("x", "user", "3")}, 1},
		{"diverge at 0", []provider.Message{textMsg("z", "user", "9")}, 0},
		{"shrunk", []provider.Message{old[0]}, 1},
		{"legacy text match", []provider.Message{textMsg("", "user", "1"), textMsg("", "assistant", "2")}, 2},
	}
	for _, tc := range cases {
		if got := persistedPrefixLen(old, tc.msgs); got != tc.want {
			t.Errorf("%s: persistedPrefixLen = %d, want %d", tc.name, got, tc.want)
		}
	}
	_ = context.Background()
}
