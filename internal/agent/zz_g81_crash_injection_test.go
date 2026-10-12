package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// G8-1 (sa-158): crash-path recovery now injects the recovery message into
// the model context (commands_slash.go), mirroring r445's Ctrl+C
// continuation injection. The mutual exclusion between the two injection
// paths is STRUCTURAL - a crashed journal is consumed+deleted by
// CheckCrashedRun (so CheckContinuation sees nothing), and an interrupted
// run has State=completed (so CheckCrashedRun returns nil). These tests
// pin that invariant so a future refactor cannot accidentally make both
// fire and double-inject on resume.

func writeTestJournal(t *testing.T, entry RunJournalEntry) string {
	t.Helper()
	sessionID := "g81-" + t.Name() + "-" + time.Now().Format("150405.000000000")
	path := journalPath(sessionID)
	t.Cleanup(func() { os.Remove(path) })
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionID
}

func TestG81CrashAndContinuationAreMutuallyExclusive(t *testing.T) {
	// Crashed run: State=running + dead PID. CheckCrashedRun consumes the
	// journal; a follow-up CheckContinuation must find nothing (the
	// journal is gone), so only the crash injection fires.
	sid := writeTestJournal(t, RunJournalEntry{
		State:      "running",
		SessionID:  "x",
		UserPrompt: "refactor the auth layer",
		PID:        999999, // dead PID (same convention as run_journal_3222_test.go)
		StartTime:  time.Now().Add(-5 * time.Minute),
	})
	if info := CheckCrashedRun(sid); info == nil {
		t.Fatal("expected crash detection for stale running journal with dead PID")
	}
	if msg := CheckContinuation(sid); msg != "" {
		t.Fatalf("continuation fired after crash consumption - double injection risk: %.80q", msg)
	}
	if _, err := os.Stat(journalPath(sid)); !os.IsNotExist(err) {
		t.Fatal("crashed journal should have been consumed (deleted)")
	}
}

func TestG81InterruptedRunDoesNotTriggerCrashPath(t *testing.T) {
	// Interrupted run: State=completed + Interrupted snapshot. The crash
	// path must return nil (clean exit per #1123 semantics) so only the
	// continuation injection fires.
	sid := writeTestJournal(t, RunJournalEntry{
		State:      "completed",
		SessionID:  "x",
		UserPrompt: "write the migration tool",
		PID:        999999, // dead PID (same convention as run_journal_3222_test.go)
		StartTime:  time.Now().Add(-5 * time.Minute),
		EndTime:    time.Now().Add(-4 * time.Minute),
		Interrupted: &InterruptSnapshot{
			Timestamp:    time.Now().Add(-4 * time.Minute),
			LastTool:     "edit_file",
			Iterations:   7,
			FilesTouched: 2,
		},
	})
	if info := CheckCrashedRun(sid); info != nil {
		t.Fatalf("crash path fired on completed+interrupted journal: %+v", info)
	}
	msg := CheckContinuation(sid)
	if msg == "" || !strings.Contains(msg, "Continuation point") {
		t.Fatalf("expected continuation message, got: %.80q", msg)
	}
}

func TestG81CrashRecoveryMessageIsModelReady(t *testing.T) {
	// The message injected via AddMessage must carry the resume-critical
	// facts: the original task and the verify-workspace-first guidance.
	msg := FormatCrashRecoveryMessage(&CrashRecoveryInfo{
		SessionID:  "s",
		StartTime:  time.Now().Add(-30 * time.Minute),
		UserPrompt: "fix the flaky reconnect test",
		PID:        4242,
		AgeHours:   0.5,
	})
	for _, want := range []string{"fix the flaky reconnect test", "git status", "may be incomplete"} {
		if !strings.Contains(msg, want) {
			t.Errorf("recovery message missing %q: %s", want, msg)
		}
	}
}
