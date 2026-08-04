package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// RunStateJournal tracks the lifecycle of agent runs to detect crashes.
//
// When an agent starts a run (RunStreamWithContent), it writes a "running"
// journal entry. When the run completes normally (success or error), it
// updates the journal to "completed". If the process crashes (SIGKILL,
// panic, power loss) during a run, the journal remains in "running" state.
//
// On the next session load, CheckCrashedRun() detects the stale "running"
// journal and returns a recovery message so the user knows their previous
// session was interrupted unexpectedly.
//
// This fills a gap identified in competitor analysis: Claude Code detects
// unexpected exits, Aider uses git checkpoints, Cursor maintains undo
// history. ggcode previously had no crash detection at all.
//
// The journal is intentionally lightweight (single JSON file per session)
// to minimize I/O overhead on the agent hot path.

const (
	journalFileName = "run_journal.json"
)

// journalDirFunc is the directory resolver. Overridable for testing.
var journalDirFunc = defaultJournalDir

func defaultJournalDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".ggcode", "journals")
	}
	return filepath.Join(configDir, "ggcode", "journals")
}

// journalDir returns the directory for journal files.
func journalDir() string {
	return journalDirFunc()
}

// RunJournalEntry represents a single run lifecycle state.
type RunJournalEntry struct {
	SessionID   string    `json:"session_id"`
	State       string    `json:"state"` // "running", "completed"
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time,omitempty"`
	PID         int       `json:"pid"`
	UserPrompt  string    `json:"user_prompt,omitempty"`
	Iterations  int       `json:"iterations,omitempty"`
	FilesEdited int       `json:"files_edited,omitempty"`
	Success     bool      `json:"success,omitempty"`
}

// RunJournal manages the run state journal file for a session.
type RunJournal struct {
	sessionDir string
}

func journalPath(sessionID string) string {
	return filepath.Join(journalDir(), sessionID+"_"+journalFileName)
}

// NewRunJournal creates a RunJournal for the given session.
func NewRunJournal(sessionID string) *RunJournal {
	return &RunJournal{
		sessionDir: journalDir(),
	}
}

// MarkRunning writes a "running" journal entry at the start of a run.
// This is the crash-detection anchor: if the process dies before
// MarkCompleted is called, the journal stays in "running" state.
func MarkRunning(sessionID, userPrompt string, pid int) {
	if sessionID == "" {
		return
	}
	dir := journalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		debug.Log("run_journal", "MarkRunning: mkdir failed: %v", err)
		return
	}

	entry := RunJournalEntry{
		SessionID:  sessionID,
		State:      "running",
		StartTime:  time.Now(),
		PID:        pid,
		UserPrompt: truncatePrompt(userPrompt, 200),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		debug.Log("run_journal", "MarkRunning: marshal failed: %v", err)
		return
	}

	path := journalPath(sessionID)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		debug.Log("run_journal", "MarkRunning: write failed: %v", err)
	}
	debug.Log("run_journal", "MarkRunning: session=%s pid=%d", sessionID, pid)
}

// MarkCompleted updates the journal to "completed" state. Safe to call
// even if MarkRunning was never called (no-op if journal doesn't exist).
func MarkCompleted(sessionID string, success bool, iterations, filesEdited int) {
	if sessionID == "" {
		return
	}
	path := journalPath(sessionID)

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		os.Remove(path)
		return
	}

	entry.State = "completed"
	entry.EndTime = time.Now()
	entry.Success = success
	entry.Iterations = iterations
	entry.FilesEdited = filesEdited

	updated, err := json.Marshal(entry)
	if err != nil {
		return
	}

	if err := os.WriteFile(path, updated, 0o644); err != nil {
		debug.Log("run_journal", "MarkCompleted: write failed: %v", err)
	}
	debug.Log("run_journal", "MarkCompleted: session=%s success=%v", sessionID, success)
}

// CrashRecoveryInfo describes a detected crashed run.
type CrashRecoveryInfo struct {
	SessionID  string    `json:"session_id"`
	StartTime  time.Time `json:"start_time"`
	UserPrompt string    `json:"user_prompt"`
	PID        int       `json:"pid"`
	AgeHours   float64   `json:"age_hours"`
}

// CheckCrashedRun checks if the given session has a stale "running" journal
// entry, indicating the previous run was interrupted by a crash. Returns
// nil if no crash is detected (journal is completed, missing, or the PID
// is still alive).
//
// The journal file is cleaned up after detection. This is a one-shot check.
func CheckCrashedRun(sessionID string) *CrashRecoveryInfo {
	if sessionID == "" {
		return nil
	}
	path := journalPath(sessionID)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		os.Remove(path)
		return nil
	}

	// Already completed: clean exit, no crash
	if entry.State == "completed" {
		if time.Since(entry.EndTime) > 24*time.Hour {
			os.Remove(path)
		}
		return nil
	}

	// State is "running": check if the PID is still alive
	if entry.PID > 0 && isProcessAlive(entry.PID) {
		// The process is still running: not a crash, just concurrent access
		return nil
	}

	// Stale "running" entry with dead PID: this is a crash
	info := &CrashRecoveryInfo{
		SessionID:  entry.SessionID,
		StartTime:  entry.StartTime,
		UserPrompt: entry.UserPrompt,
		PID:        entry.PID,
		AgeHours:   time.Since(entry.StartTime).Hours(),
	}

	// Clean up the stale journal
	os.Remove(path)

	debug.Log("run_journal", "CheckCrashedRun: detected crash session=%s age=%.1fh",
		sessionID, info.AgeHours)

	return info
}

// FormatCrashRecoveryMessage formats a human-readable crash recovery message
// that the agent can inject as context when resuming a crashed session.
func FormatCrashRecoveryMessage(info *CrashRecoveryInfo) string {
	if info == nil {
		return ""
	}

	ageStr := formatDuration(time.Duration(info.AgeHours * float64(time.Hour)))

	return fmt.Sprintf(
		"[Crash Recovery] Your previous session was interrupted unexpectedly (crashed or killed) "+
			"approximately %s ago. The last task was: %q. "+
			"Review any uncommitted file changes with git status or git diff before continuing, "+
			"as some edits from the interrupted run may be incomplete.",
		ageStr, info.UserPrompt)
}

// CleanupOldJournals removes journal files older than maxAge. Called at
// startup to prevent unbounded journal accumulation.
func CleanupOldJournals(maxAge time.Duration) {
	dir := journalDir()
	matches, err := filepath.Glob(filepath.Join(dir, "*_"+journalFileName))
	if err != nil || len(matches) == 0 {
		return
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(path)
			removed++
		}
	}
	if removed > 0 {
		debug.Log("run_journal", "CleanupOldJournals: removed %d stale journals", removed)
	}
}

// isProcessAlive checks if a process with the given PID is running.
// Uses signal 0 (existence check) on Unix; on Windows always returns false
// (journal detection falls back to PID-based heuristics).
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks process existence without sending a real signal
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
