package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindMessageCutoff_AnchorsOnLastDialogue(t *testing.T) {
	now := time.Now()

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

	lines = append(lines, fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":"system","content":"[checkpoint note]"}}`+"\n", now.Add(-1*time.Hour).Format(time.RFC3339Nano))...)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, lines, 0600); err != nil {
		t.Fatal(err)
	}

	cutoff, total, _ := findMessageCutoff(path)
	if total != 506 {
		t.Fatalf("total messages = %d, want 506", total)
	}

	if cutoff > firstDialogueOffset {
		t.Fatalf("cutoff offset %d excludes first dialogue message at %d - window anchored on system tail, not last user/assistant message", cutoff, firstDialogueOffset)
	}

	if cutoff != 0 {
		t.Fatalf("expected full-load cutoff 0 (all messages within anchored window), got %d", cutoff)
	}
}

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

	if cutoff <= 0 {
		t.Fatalf("expected a windowed cutoff > 0, got %d", cutoff)
	}
}
