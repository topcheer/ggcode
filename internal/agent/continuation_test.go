package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r445 continuation-point probes: interrupt snapshot round-trip, one-shot
// consumption, freshness window, corrupt-journal safety.

func markRunForTest(t *testing.T, sid string) {
	t.Helper()
	MarkRunning(sid, "original task text", os.Getpid())
	MarkCompleted(sid, false, 3, 2)
}

func TestContinuationRoundTripAndOneShot(t *testing.T) {
	dir, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "cont-roundtrip"
	markRunForTest(t, sid)

	MarkInterrupted(sid, InterruptSnapshot{
		Timestamp:    time.Now(),
		LastTool:     "edit_file",
		Iterations:   4,
		FilesTouched: 2,
	})

	msg := CheckContinuation(sid)
	if msg == "" {
		t.Fatal("fresh interrupt snapshot must produce a continuation message")
	}
	for _, want := range []string{"interrupted", "edit_file", "4 iteration", "2 file", "original task text"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "crash") {
		t.Errorf("wording must never assert crash (#1123): %s", msg)
	}

	// One-shot: second read must be empty (snapshot consumed).
	if again := CheckContinuation(sid); again != "" {
		t.Fatalf("snapshot must be consumed on first read, got: %s", again)
	}
	// And the persisted journal no longer carries the snapshot.
	raw, err := os.ReadFile(filepath.Join(dir, sid+"_run_journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "\"interrupted\"") {
		t.Fatalf("consumed journal must not keep the snapshot: %s", raw)
	}
}

func TestContinuationStaleSnapshotDiscarded(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "cont-stale"
	markRunForTest(t, sid)

	MarkInterrupted(sid, InterruptSnapshot{
		Timestamp: time.Now().Add(-3 * time.Hour), // beyond 2h window
		LastTool:  "run_command",
	})
	if msg := CheckContinuation(sid); msg != "" {
		t.Fatalf("stale snapshot must return empty, got: %s", msg)
	}
	// Stale means discard-on-read, not re-offer.
	if again := CheckContinuation(sid); again != "" {
		t.Fatalf("stale snapshot must be consumed even when discarded: %s", again)
	}
}

func TestContinuationNoSnapshotNoMessage(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "cont-clean"
	markRunForTest(t, sid)
	if msg := CheckContinuation(sid); msg != "" {
		t.Fatalf("clean run must yield no continuation, got: %s", msg)
	}
	if msg := CheckContinuation("cont-never-existed"); msg != "" {
		t.Fatalf("missing journal must yield empty, got: %s", msg)
	}
}

func TestMarkInterruptedCorruptJournalPreserved(t *testing.T) {
	dir, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "cont-corrupt"
	path := filepath.Join(dir, sid+"_run_journal.json")
	if err := os.WriteFile(path, []byte("{torn json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Must not panic and must not overwrite the corrupt evidence (#1666).
	MarkInterrupted(sid, InterruptSnapshot{LastTool: "x"})
	raw, _ := os.ReadFile(path)
	if string(raw) != "{torn json" {
		t.Fatalf("corrupt journal must be preserved verbatim, got: %s", raw)
	}
	if msg := CheckContinuation(sid); msg != "" {
		t.Fatalf("corrupt journal yields no continuation, got: %s", msg)
	}
}

func TestContinuationSnapshotJSONCompat(t *testing.T) {
	// Pre-r445 journal (no interrupted field) must still unmarshal.
	var entry RunJournalEntry
	legacy := `{"session_id":"s1","state":"completed","start_time":"2026-01-01T00:00:00Z","pid":1,"iterations":2}`
	if err := json.Unmarshal([]byte(legacy), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Interrupted != nil {
		t.Fatal("legacy journal must load with nil Interrupted")
	}
}
