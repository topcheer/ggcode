package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckContinuation_SkipsSuccessStampedEntry(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	sid := "issue3220-success-race"

	MarkRunning(sid, "long task", 4242)
	MarkCompleted(sid, true, 7, 3)
	MarkInterrupted(sid, InterruptSnapshot{
		Timestamp:    time.Now(),
		LastTool:     "edit_file",
		Iterations:   7,
		FilesTouched: 3,
	})

	msg := CheckContinuation(sid)
	if msg != "" {
		t.Fatalf("success-stamped run must not offer a continuation point, got: %q", msg)
	}

	path := filepath.Join(journalDirFunc(), sid+"_"+journalFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("journal file not found: %v", err)
	}
	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid journal JSON: %v", err)
	}
	if entry.Interrupted != nil {
		t.Error("snapshot must be consumed on read even when skipped for success")
	}
}

func TestCheckContinuation_RealInterruptStillOffered(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	sid := "issue3220-real-interrupt"
	MarkRunning(sid, "long task", 4242)
	MarkCompleted(sid, false, 7, 3)
	MarkInterrupted(sid, InterruptSnapshot{
		Timestamp:    time.Now(),
		LastTool:     "edit_file",
		Iterations:   7,
		FilesTouched: 3,
	})

	msg := CheckContinuation(sid)
	if msg == "" {
		t.Fatal("genuine interruption must still offer a continuation point")
	}
	if !strings.Contains(msg, "interrupted mid-flight") {
		t.Errorf("unexpected message shape: %q", msg)
	}
	if !strings.Contains(msg, "long task") {
		t.Errorf("message should carry the original task prompt, got: %q", msg)
	}
}
