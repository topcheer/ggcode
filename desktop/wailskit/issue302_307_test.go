package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// #305: persisting to a tombstoned (deleted) session must be refused —
// the file must not be re-created on disk by a draining run goroutine.
func TestAppendPersistMessage_DeletedSessionRefused(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ses := &session.Session{ID: "deleted-ses"}
	b, err2 := NewChatBridge()
	if err2 != nil {
		t.Fatalf("NewChatBridge: %v", err2)
	}
	b.MarkSessionDeleted(ses.ID)

	b.appendPersistMessage(store, ses, provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "late"}}})

	// The JSONL must NOT have been re-created on disk.
	if _, statErr := os.Stat(filepath.Join(dir, ses.ID+".jsonl")); statErr == nil {
		t.Fatalf("deleted session was resurrected on disk by late persist")
	}
}

// #305: MarkSessionDeleted must be idempotent and tolerate empty IDs.
func TestMarkSessionDeleted_Idempotent(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	b.MarkSessionDeleted("")
	b.MarkSessionDeleted("x")
	b.MarkSessionDeleted("x")
	b.mu.Lock()
	n := len(b.deletedSessions)
	b.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 tombstone, got %d", n)
	}
}

// #305: ClearCurrentSession must drop the run-start snapshot so a late
// persist from a draining run cannot target the deleted session.
func TestClearCurrentSession_NilsRunSes(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Fatalf("NewChatBridge: %v", err)
	}
	b.mu.Lock()
	b.runSes = &session.Session{ID: "run-ses"}
	b.mu.Unlock()
	b.ClearCurrentSession()
	b.mu.Lock()
	dead := b.runSes == nil
	b.mu.Unlock()
	if !dead {
		t.Fatalf("ClearCurrentSession left runSes set")
	}
}

// #307: truncation must be valid UTF-8 for every crossing layout of a
// 3-byte CJK char (E6 B1 89) across the 2000-byte boundary, plus a 4-byte
// emoji (F0 9F 98 80) across the boundary in 4 layouts.
func TestFormatMessagesAsMarkdown_TruncationRuneBoundary(t *testing.T) {
	mk := func(fill int, tail string) SessionMessage {
		return SessionMessage{
			Role:    "tool",
			Content: strings.Repeat("a", fill) + tail + strings.Repeat("b", 10),
		}
	}
	// 3-byte char crossing: lead at 1998, 1999, or fully inside.
	for _, fill := range []int{1997, 1998, 1999, 1996} {
		out := formatMessagesAsMarkdown([]SessionMessage{mk(fill, "\u6c49")}, "t")
		if !utf8.ValidString(out) {
			t.Errorf("3-byte layout fill=%d produced invalid UTF-8 output", fill)
		}
	}
	// 4-byte emoji crossing in 4 layouts.
	for _, fill := range []int{1996, 1997, 1998, 1999} {
		out := formatMessagesAsMarkdown([]SessionMessage{mk(fill, "\U0001f600")}, "t")
		if !utf8.ValidString(out) {
			t.Errorf("4-byte layout fill=%d produced invalid UTF-8 output", fill)
		}
	}
}
