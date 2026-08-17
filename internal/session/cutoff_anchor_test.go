package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFindMessageCutoff_AnchorsOnLastDialogue reproduces #601: long-running
// sessions end with a tail of system messages (checkpoint notes, resume
// markers) appended long after the last real exchange. The old cutoff
// anchored the 24h RecentMessageWindow on the last message of ANY role, so
// the entire window covered only system messages and the TUI rendered zero
// visible history (real-world case: 21,815 messages, 2 in window, both
// system). The window must anchor on the last user/assistant message.
func TestFindMessageCutoff_AnchorsOnLastDialogue(t *testing.T) {
	now := time.Now()

	// Build a file past recentMessageThreshold: 505 dialogue messages
	// ending 30h ago, then a system-note tail written 1h ago.
	var lines []byte
	var firstDialogueOffset int64
	for i := 0; i < 505; i++ {
		ts := now.Add(-100 * time.Hour).Add(time.Duration(i) * time.Minute)
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		line := fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":%q,"content":"m%d"}}`+"\n", ts.Format(time.RFC3339Nano), role, i)
		if i == 0 {
			firstDialogueOffset = int64(len(lines))
		}
		lines = append(lines, line...)
	}
	// System tail: recent, but NOT the conversation anchor.
	lines = append(lines, fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":"system","content":"[checkpoint note]"}}`+"\n", now.Add(-1*time.Hour).Format(time.RFC3339Nano))...)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, lines, 0600); err != nil {
		t.Fatal(err)
	}

	cutoff, total, _ := findMessageCutoff(path)
	if total != 506 {
		t.Fatalf("total messages = %d, want 506", total)
	}
	// The first dialogue message must fall inside the window. With the old
	// system-anchored cutoff (now-24h), all 505 dialogue messages were
	// excluded and only the system tail survived.
	if cutoff > firstDialogueOffset {
		t.Fatalf("cutoff offset %d excludes first dialogue message at %d - window anchored on system tail, not last user/assistant message", cutoff, firstDialogueOffset)
	}

	// Sanity: window anchored 30h+24h back means everything loads.
	if cutoff != 0 {
		t.Fatalf("expected full-load cutoff 0 (all messages within anchored window), got %d", cutoff)
	}
}

// TestFindMessageCutoff_SystemOnlyTailStillWorks ensures a file whose only
// timestamps are on system messages (no dialogue at all) still produces a
// usable window instead of degrading to zero.
func TestFindMessageCutoff_SystemOnlyFallback(t *testing.T) {
	now := time.Now()
	var lines []byte
	for i := 0; i < 510; i++ {
		role := "system"
		ts := now.Add(-100 * time.Hour)
		if i >= 505 {
			role = "user"
			ts = now.Add(-1 * time.Hour)
		}
		if i < 505 {
			ts = now.Add(-100 * time.Hour).Add(time.Duration(i) * time.Minute)
		}
		lines = append(lines, fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":%q,"content":"m%d"}}`+"\n", ts.Format(time.RFC3339Nano), role, i)...)
	}

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, lines, 0600); err != nil {
		t.Fatal(err)
	}

	cutoff, total, last := findMessageCutoff(path)
	if total != 510 {
		t.Fatalf("total messages = %d, want 510", total)
	}
	if last.IsZero() {
		t.Fatal("expected non-zero anchor timestamp")
	}
	// Dialogue at now-1h: window covers it plus the recent system messages.
	if cutoff <= 0 {
		t.Fatalf("expected a windowed cutoff > 0, got %d", cutoff)
	}
}
