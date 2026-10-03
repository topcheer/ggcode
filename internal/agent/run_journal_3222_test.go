package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #3222 probes: both silent under-reporting paths of crash recovery.

// TestCheckCrashedRun_OldRunningJournalWithLivePID: a running journal older
// than crashLivePIDTrustWindow whose PID happens to be alive (PID reuse after
// reboot) must be reported as a crash suspect, not silently treated as
// concurrent access.
func TestCheckCrashedRun_OldRunningJournalWithLivePID(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	dir := journalDirFunc()
	path := filepath.Join(dir, "reused-session_"+journalFileName)

	// Our own test process PID is guaranteed alive - simulates an unrelated
	// process holding the crashed run's PID after a reboot.
	entry := RunJournalEntry{
		SessionID:  "reused-session",
		State:      "running",
		StartTime:  time.Now().Add(-5 * time.Hour), // > 4h trust window
		PID:        os.Getpid(),
		UserPrompt: "pid reuse probe",
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(path, data, 0o644)

	info := CheckCrashedRun("reused-session")
	if info == nil {
		t.Fatal("old running journal with live (reused) PID must be reported, got nil")
	}
	if info.SessionID != "reused-session" {
		t.Errorf("session_id = %q", info.SessionID)
	}
	// Reported = consumed: journal removed like the normal crash path.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("journal should be removed after crash-suspect report")
	}
}

// TestCheckCrashedRun_YoungRunningJournalWithLivePID: a young running journal
// with a live PID is genuine concurrent access - unchanged, returns nil.
func TestCheckCrashedRun_YoungRunningJournalWithLivePID(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	dir := journalDirFunc()
	path := filepath.Join(dir, "concurrent-session_"+journalFileName)
	entry := RunJournalEntry{
		SessionID: "concurrent-session",
		State:     "running",
		StartTime: time.Now().Add(-10 * time.Minute), // < 4h trust window
		PID:       os.Getpid(),
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(path, data, 0o644)

	if info := CheckCrashedRun("concurrent-session"); info != nil {
		t.Fatalf("young live-PID journal is concurrent access, got report: %+v", info)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("concurrent-access journal must be preserved")
	}
}

func writeJournalFile(t *testing.T, dir, session, state string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, session+"_"+journalFileName)
	entry := RunJournalEntry{
		SessionID: session,
		State:     state,
		StartTime: time.Now().Add(-age),
		PID:       999999, // dead
	}
	data, _ := json.Marshal(entry)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-age)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCleanupOldJournals_RetainsRunningState: a >24h running journal must
// survive cleanup so /resume can still report it (path B fix).
func TestCleanupOldJournals_RetainsRunningState(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	dir := journalDirFunc()

	runningPath := writeJournalFile(t, dir, "late-resume", "running", 30*24*time.Hour/10) // 3 days
	// 3 days > 24h maxAge but < 7d hard delete: must be retained.
	CleanupOldJournals(24 * time.Hour)
	if _, err := os.Stat(runningPath); err != nil {
		t.Fatal("running journal older than maxAge must be retained for crash reporting")
	}
}

// TestCleanupOldJournals_RemovesCompletedAndExpiredRunning: completed files
// go at maxAge; running files go only at the hard-delete window.
func TestCleanupOldJournals_RemovesCompletedAndExpiredRunning(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	dir := journalDirFunc()

	completedPath := writeJournalFile(t, dir, "done-old", "completed", 2*24*time.Hour)
	expiredPath := writeJournalFile(t, dir, "ancient", "running", 8*24*time.Hour)
	youngPath := writeJournalFile(t, dir, "young", "completed", 1*time.Hour)

	CleanupOldJournals(24 * time.Hour)

	if _, err := os.Stat(completedPath); !os.IsNotExist(err) {
		t.Error("completed journal older than maxAge must be removed")
	}
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Error("running journal past hard-delete window must be removed (leak guard)")
	}
	if _, err := os.Stat(youngPath); err != nil {
		t.Error("young journal must be untouched")
	}
}

// TestCleanupOldJournals_RetainsCorruptEvidence: a >24h corrupt journal is
// crash-suspect evidence (#1666) and gets the running-state retention grace.
func TestCleanupOldJournals_RetainsCorruptEvidence(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	dir := journalDirFunc()

	path := filepath.Join(dir, "torn-write_"+journalFileName)
	os.WriteFile(path, []byte(`{"state":"run`), 0o644) // torn JSON
	past := time.Now().Add(-3 * 24 * time.Hour)
	os.Chtimes(path, past, past)

	CleanupOldJournals(24 * time.Hour)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("corrupt journal within hard-delete window must be retained as evidence")
	}
}
