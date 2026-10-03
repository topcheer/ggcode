package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCheckContinuation_SkipsSuccessStampedEntry pins #3220: a run that
// completed successfully can still carry an Interrupted snapshot when the
// user's Ctrl+C landed in the finalize window after the run already
// returned nil (isCancelled's second disjunct fires on err==nil with a
// cancelled ctx). The resume must NOT inject a "continue where it left
// off" message for finished work, and the contradictory snapshot must be
// consumed (cleared), not left to replay on a later resume.
func TestCheckContinuation_SkipsSuccessStampedEntry(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	sid := "issue3220-success-race"
	// Reproduce the race order: MarkRunning -> MarkCompleted(success=true,
	// agent.go passes err==nil) -> MarkInterrupted stamped by the
	// isCancelled misjudge.
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

	// The contradictory snapshot must be consumed even when skipped.
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

// TestCheckContinuation_RealInterruptStillOffered guards the other side of
// #3220: a genuine mid-flight Ctrl+C (agent.go passes err=context.Canceled,
// MarkCompleted records success=false) must still surface the continuation
// point within the fresh window.
func TestCheckContinuation_RealInterruptStillOffered(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	sid := "issue3220-real-interrupt"
	MarkRunning(sid, "long task", 4242)
	MarkCompleted(sid, false, 7, 3) // cancelled run records success=false
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
