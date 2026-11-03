package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	journalFileName = "run_journal.json"

	crashLivePIDTrustWindow = 4 * time.Hour

	runningJournalHardDelete = 7 * 24 * time.Hour
)

var journalDirFunc = defaultJournalDir

func defaultJournalDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".ggcode", "journals")
	}
	return filepath.Join(configDir, "ggcode", "journals")
}

func journalDir() string {
	return journalDirFunc()
}

type RunJournalEntry struct {
	SessionID   string             `json:"session_id"`
	State       string             `json:"state"`
	StartTime   time.Time          `json:"start_time"`
	EndTime     time.Time          `json:"end_time,omitempty"`
	PID         int                `json:"pid"`
	UserPrompt  string             `json:"user_prompt,omitempty"`
	Iterations  int                `json:"iterations,omitempty"`
	FilesEdited int                `json:"files_edited,omitempty"`
	Success     bool               `json:"success,omitempty"`
	Interrupted *InterruptSnapshot `json:"interrupted,omitempty"`
}

type InterruptSnapshot struct {
	Timestamp    time.Time `json:"timestamp"`
	LastTool     string    `json:"last_tool,omitempty"`
	Iterations   int       `json:"iterations,omitempty"`
	FilesTouched int       `json:"files_touched,omitempty"`
	UserPrompt   string    `json:"user_prompt,omitempty"`
}

func journalPath(sessionID string) string {
	return filepath.Join(journalDir(), sessionID+"_"+journalFileName)
}

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

	_ = os.Remove(crashSidecarPath(sessionID))
	if err := atomicWriteJournal(path, data); err != nil {
		debug.Log("run_journal", "MarkRunning: write failed: %v", err)
	}
	debug.Log("run_journal", "MarkRunning: session=%s pid=%d", sessionID, pid)
}

func atomicWriteJournal(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func MarkCompleted(sessionID string, success bool, iterations, filesEdited int) {
	if sessionID == "" {
		return
	}

	_ = os.Remove(crashSidecarPath(sessionID))
	path := journalPath(sessionID)

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {

		debug.Log("run_journal", "MarkCompleted: journal corrupt (%v) - preserved", err)
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

	if err := atomicWriteJournal(path, updated); err != nil {
		debug.Log("run_journal", "MarkCompleted: write failed: %v", err)
	}
	debug.Log("run_journal", "MarkCompleted: session=%s success=%v", sessionID, success)
}

type CrashRecoveryInfo struct {
	SessionID  string    `json:"session_id"`
	StartTime  time.Time `json:"start_time"`
	UserPrompt string    `json:"user_prompt"`
	PID        int       `json:"pid"`
	AgeHours   float64   `json:"age_hours"`
}

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

		debug.Log("run_journal", "CheckCrashedRun: journal corrupt (%v) - preserved, reporting as crash suspect", err)
		return &CrashRecoveryInfo{
			SessionID:  sessionID,
			UserPrompt: fmt.Sprintf("<journal corrupted: %s>", filepath.Base(path)),
		}
	}

	if entry.State == "completed" {
		if time.Since(entry.EndTime) > 24*time.Hour {
			os.Remove(path)
		}
		return nil
	}

	journalAge := time.Since(entry.StartTime)
	if entry.PID > 0 && isProcessAlive(entry.PID) && journalAge < crashLivePIDTrustWindow {

		return nil
	}
	if entry.PID > 0 && isProcessAlive(entry.PID) {
		debug.Log("run_journal", "CheckCrashedRun: running journal age=%.1fh exceeds trust window despite live PID %d - treating as PID-reuse crash suspect", journalAge.Hours(), entry.PID)
	}

	info := &CrashRecoveryInfo{
		SessionID:  entry.SessionID,
		StartTime:  entry.StartTime,
		UserPrompt: entry.UserPrompt,
		PID:        entry.PID,
		AgeHours:   time.Since(entry.StartTime).Hours(),
	}

	os.Remove(path)

	debug.Log("run_journal", "CheckCrashedRun: detected crash session=%s age=%.1fh",
		sessionID, info.AgeHours)

	return info
}

func FormatCrashRecoveryMessage(info *CrashRecoveryInfo) string {
	if info == nil {
		return ""
	}

	ageStr := formatDuration(time.Duration(info.AgeHours * float64(time.Hour)))

	return fmt.Sprintf(

		"[Crash Recovery] Your previous session did not exit cleanly "+
			"(aborted, crashed, or the terminal closed are all possible) "+
			"approximately %s ago. The last task was: %q. "+
			"Review any uncommitted file changes with git status or git diff before continuing, "+
			"as some edits from the interrupted run may be incomplete.",
		ageStr, info.UserPrompt)
}

func MarkInterrupted(sessionID string, snap InterruptSnapshot) {
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

		return
	}
	if snap.Timestamp.IsZero() {
		snap.Timestamp = time.Now()
	}
	entry.Interrupted = &snap
	updated, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if err := atomicWriteJournal(path, updated); err != nil {
		debug.Log("run_journal", "MarkInterrupted: write failed: %v", err)
	}
}

const continuationFreshWindow = 2 * time.Hour

func CheckContinuation(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	path := journalPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return ""
	}
	snap := entry.Interrupted
	if snap == nil {
		return ""
	}

	entry.Interrupted = nil
	if updated, merr := json.Marshal(entry); merr == nil {
		if werr := atomicWriteJournal(path, updated); werr != nil {
			debug.Log("run_journal", "CheckContinuation: consume write failed: %v", werr)
		}
	}

	if entry.Success {
		return ""
	}
	if time.Since(snap.Timestamp) > continuationFreshWindow {
		return ""
	}
	return formatContinuationMessage(&entry, snap)
}

func formatContinuationMessage(entry *RunJournalEntry, snap *InterruptSnapshot) string {
	var b strings.Builder
	b.WriteString("Continuation point: the previous run in this session was interrupted mid-flight")
	if snap.Iterations > 0 {
		fmt.Fprintf(&b, " after %d iteration(s)", snap.Iterations)
	}
	if snap.LastTool != "" {
		fmt.Fprintf(&b, "; last tool in flight: %s", snap.LastTool)
	}
	if snap.FilesTouched > 0 {
		fmt.Fprintf(&b, "; %d file(s) already touched", snap.FilesTouched)
	}
	b.WriteString(". Tool results for cancelled calls show as placeholders - re-check the touched files' current state before continuing, then resume the task where it left off rather than restarting from scratch.")
	if entry.UserPrompt != "" {
		fmt.Fprintf(&b, "\nOriginal task: %s", entry.UserPrompt)
	}
	return b.String()
}

func CleanupOldJournals(maxAge time.Duration) {
	dir := journalDir()
	matches, err := filepath.Glob(filepath.Join(dir, "*_"+journalFileName))
	if err != nil || len(matches) == 0 {
		return
	}

	now := time.Now()
	cutoff := now.Add(-maxAge)
	hardCutoff := now.Add(-runningJournalHardDelete)
	removed := 0
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if info.ModTime().After(hardCutoff) && journalStateUncertain(path) {

			continue
		}
		os.Remove(path)
		removed++
	}
	if removed > 0 {
		debug.Log("run_journal", "CleanupOldJournals: removed %d stale journals", removed)
	}
}

func journalStateUncertain(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var entry RunJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return true
	}
	return entry.State == "running"
}

func isProcessAlive(pid int) bool {
	return util.IsProcessAlive(pid)
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
