package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	sid := writeTestJournal(t, RunJournalEntry{
		State:      "running",
		SessionID:  "x",
		UserPrompt: "refactor the auth layer",
		PID:        999999,
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

	sid := writeTestJournal(t, RunJournalEntry{
		State:      "completed",
		SessionID:  "x",
		UserPrompt: "write the migration tool",
		PID:        999999,
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
