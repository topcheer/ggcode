package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// journalDirForTest creates a temp journal dir and returns a cleanup func.
func journalDirForTest(t *testing.T) (dir string, cleanup func()) {
	t.Helper()
	dir = t.TempDir()

	// Override journalDir by setting TMPDIR-like mechanism: we monkey-patch
	// via a package-level override for testing.
	orig := journalDirFunc
	journalDirFunc = func() string { return dir }
	return dir, func() {
		journalDirFunc = orig
	}
}

func TestMarkRunning_CreatesJournal(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	MarkRunning("test-session-1", "fix the bug", 12345)

	path := filepath.Join(t.TempDir(), "test-session-1_"+journalFileName)
	// journalDirFunc was overridden, so re-read from the override
	dir := journalDirFunc()
	path = filepath.Join(dir, "test-session-1_"+journalFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("journal file not created: %v", err)
	}

	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if entry.State != "running" {
		t.Errorf("state = %q, want %q", entry.State, "running")
	}
	if entry.SessionID != "test-session-1" {
		t.Errorf("session_id = %q, want %q", entry.SessionID, "test-session-1")
	}
	if entry.PID != 12345 {
		t.Errorf("pid = %d, want 12345", entry.PID)
	}
	if entry.UserPrompt != "fix the bug" {
		t.Errorf("user_prompt = %q, want %q", entry.UserPrompt, "fix the bug")
	}
}

func TestMarkCompleted_UpdatesState(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	MarkRunning("test-session-2", "write tests", 99999)
	MarkCompleted("test-session-2", true, 5, 3)

	dir := journalDirFunc()
	path := filepath.Join(dir, "test-session-2_"+journalFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("journal file not found: %v", err)
	}

	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if entry.State != "completed" {
		t.Errorf("state = %q, want %q", entry.State, "completed")
	}
	if !entry.Success {
		t.Errorf("success = false, want true")
	}
	if entry.Iterations != 5 {
		t.Errorf("iterations = %d, want 5", entry.Iterations)
	}
	if entry.FilesEdited != 3 {
		t.Errorf("files_edited = %d, want 3", entry.FilesEdited)
	}
	if entry.EndTime.IsZero() {
		t.Error("end_time should be set")
	}
}

func TestCheckCrashedRun_DetectsCrash(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	dir := journalDirFunc()
	path := filepath.Join(dir, "crashed-session_"+journalFileName)

	// Write a stale "running" entry with a dead PID
	entry := RunJournalEntry{
		SessionID:  "crashed-session",
		State:      "running",
		StartTime:  time.Now().Add(-2 * time.Hour),
		PID:        999999, // very unlikely to exist
		UserPrompt: "complex refactor",
	}
	data, _ := json.Marshal(entry)
	os.WriteFile(path, data, 0o644)

	info := CheckCrashedRun("crashed-session")
	if info == nil {
		t.Fatal("expected crash detection, got nil")
	}

	if info.SessionID != "crashed-session" {
		t.Errorf("session_id = %q", info.SessionID)
	}
	if info.UserPrompt != "complex refactor" {
		t.Errorf("user_prompt = %q", info.UserPrompt)
	}
	if info.AgeHours < 1.0 {
		t.Errorf("age_hours = %.1f, want > 1.0", info.AgeHours)
	}

	// Journal should be cleaned up after detection
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("journal file should be removed after crash detection")
	}
}

func TestCheckCrashedRun_NoCrashForCompleted(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	MarkRunning("completed-session", "done task", 12345)
	MarkCompleted("completed-session", true, 3, 1)

	info := CheckCrashedRun("completed-session")
	if info != nil {
		t.Fatalf("expected nil for completed session, got %+v", info)
	}
}

func TestCheckCrashedRun_NoJournalReturnsNil(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	info := CheckCrashedRun("never-existed")
	if info != nil {
		t.Fatalf("expected nil for missing journal, got %+v", info)
	}
}

func TestCheckCrashedRun_EmptySessionIDReturnsNil(t *testing.T) {
	info := CheckCrashedRun("")
	if info != nil {
		t.Fatal("expected nil for empty session ID")
	}
}

func TestFormatCrashRecoveryMessage(t *testing.T) {
	info := &CrashRecoveryInfo{
		SessionID:  "test-session",
		StartTime:  time.Now().Add(-30 * time.Minute),
		UserPrompt: "implement feature X",
		PID:        12345,
		AgeHours:   0.5,
	}

	msg := FormatCrashRecoveryMessage(info)
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if msg[:16] != "[Crash Recovery]" {
		t.Errorf("message should start with [Crash Recovery], got: %s", msg[:20])
	}
	// #1123: the message must not assert "crashed or killed" as fact - the
	// detection only proves the run did not exit cleanly (deliberate stops
	// included). Factual wording only.
	if strings.Contains(msg, "crashed or killed") {
		t.Errorf("message must not assert crash cause as fact, got: %s", msg)
	}
	if !strings.Contains(msg, "did not exit cleanly") {
		t.Errorf("message should state the factual observation, got: %s", msg)
	}
}

func TestFormatCrashRecoveryMessage_NilInfo(t *testing.T) {
	msg := FormatCrashRecoveryMessage(nil)
	if msg != "" {
		t.Errorf("expected empty string for nil, got %q", msg)
	}
}

func TestMarkRunning_EmptySessionIDNoOp(t *testing.T) {
	dir := t.TempDir()
	orig := journalDirFunc
	journalDirFunc = func() string { return dir }
	defer func() { journalDirFunc = orig }()

	MarkRunning("", "prompt", 123)

	matches, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(matches) != 0 {
		t.Errorf("expected no journal files for empty session ID")
	}
}

func TestMarkCompleted_NoJournalFile(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()

	// Should not panic or error when journal doesn't exist
	MarkCompleted("nonexistent-session", true, 1, 0)
}

func TestCleanupOldJournals(t *testing.T) {
	dir := t.TempDir()
	orig := journalDirFunc
	journalDirFunc = func() string { return dir }
	defer func() { journalDirFunc = orig }()

	// Create an old journal (2 days ago)
	oldEntry := RunJournalEntry{
		SessionID: "old-session",
		State:     "completed",
		EndTime:   time.Now().Add(-48 * time.Hour),
	}
	oldPath := filepath.Join(dir, "old-session_"+journalFileName)
	data, _ := json.Marshal(oldEntry)
	os.WriteFile(oldPath, data, 0o644)

	// Backdate the file mod time
	oldTime := time.Now().Add(-48 * time.Hour)
	os.Chtimes(oldPath, oldTime, oldTime)

	// Create a recent journal
	MarkRunning("recent-session", "task", 123)

	CleanupOldJournals(24 * time.Hour)

	// Old journal should be removed
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old journal should be removed")
	}

	// Recent journal should remain
	recentPath := filepath.Join(dir, "recent-session_"+journalFileName)
	if _, err := os.Stat(recentPath); os.IsNotExist(err) {
		t.Error("recent journal should still exist")
	}
}

func TestMarkCompleted_CorruptedJournal(t *testing.T) {
	dir := t.TempDir()
	orig := journalDirFunc
	journalDirFunc = func() string { return dir }
	defer func() { journalDirFunc = orig }()

	path := filepath.Join(dir, "corrupt_"+journalFileName)
	os.WriteFile(path, []byte("{invalid json"), 0o644)

	// Should not panic
	MarkCompleted("corrupt", true, 1, 0)

	// Corrupted file should be cleaned up
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("corrupted journal should be removed")
	}
}

// Regression for #1490: isProcessAlive's local Signal(0) implementation
// returned false for LIVE processes on Windows (EWINDOWS), so a concurrent
// /resume misjudged the instance as crashed and removed its live anchor.
// The delegation to util.IsProcessAlive must report the current process alive.
func TestIsProcessAliveCurrentProcess(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("current process must be reported alive")
	}
	if isProcessAlive(-1) {
		t.Fatal("invalid pid must not be alive")
	}
}
