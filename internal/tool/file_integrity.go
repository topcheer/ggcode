package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileIntegrityTracker provides optimistic concurrency control for file edits.
// It tracks file modification times (mtimes) as observed by read_file, edit_file,
// and write_file to detect stale reads — files modified externally since the
// agent last observed them.
//
// In multi-agent workflows (spawn_agent, swarm teammates, concurrent user edits),
// without this guard, write_file silently overwrites external changes, causing
// lost updates. The tracker enables detection and prevention of this class of
// data-loss bugs.
//
// The tracker is process-scoped: all agents running in the same process share
// the same tracker. This is correct for the common case (sub-agents are
// goroutines in the parent process).
type FileIntegrityTracker struct {
	mu       sync.RWMutex
	modtimes map[string]time.Time // normalized path → last known mtime
}

// defaultFileTracker is the package-level singleton used by all file tools.
var defaultFileTracker = NewFileIntegrityTracker()

// NewFileIntegrityTracker creates a new tracker instance.
func NewFileIntegrityTracker() *FileIntegrityTracker {
	return &FileIntegrityTracker{
		modtimes: make(map[string]time.Time),
	}
}

// normalizePath resolves a path to its absolute, cleaned form for consistent
// map keys regardless of how the caller referenced the file.
func normalizePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

// RecordRead records the current mtime of a file after it has been successfully
// read. This establishes a baseline for staleness detection.
func (t *FileIntegrityTracker) RecordRead(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return // file doesn't exist or inaccessible; nothing to track
	}
	key := normalizePath(path)
	t.mu.Lock()
	t.modtimes[key] = info.ModTime()
	t.mu.Unlock()
}

// RecordWrite records the mtime of a file after it has been successfully written
// or edited by the agent. This updates the baseline so that subsequent stale
// checks are relative to the agent's own write, not a previous external write.
func (t *FileIntegrityTracker) RecordWrite(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	key := normalizePath(path)
	t.mu.Lock()
	t.modtimes[key] = info.ModTime()
	t.mu.Unlock()
}

// CheckStale returns whether the file was modified on disk since the last
// recorded read or write by the agent. Returns (false, zero) if the file was
// never tracked (first encounter — cannot determine staleness).
//
// If stale is true, since is the last-known mtime from the agent's perspective.
// The caller should warn or block the operation and instruct the agent to
// re-read the file.
func (t *FileIntegrityTracker) CheckStale(path string) (stale bool, since time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return false, time.Time{} // file doesn't exist; not stale
	}
	key := normalizePath(path)
	t.mu.RLock()
	lastKnown, ok := t.modtimes[key]
	t.mu.RUnlock()
	if !ok {
		return false, time.Time{} // never tracked
	}
	current := info.ModTime()
	if current.After(lastKnown) {
		return true, lastKnown
	}
	return false, time.Time{}
}

// HasBeenSeen reports whether the file at path has ever been recorded by the
// agent (via read_file, edit_file, write_file, or multi_file_write) in the
// current process. Returns false for files the agent has never interacted
// with, even if they exist on disk.
//
// This is used by write_file and multi_file_write to detect "blind overwrites"
// — cases where the agent is about to overwrite an existing file whose current
// contents it has never seen, risking silent data loss.
func (t *FileIntegrityTracker) HasBeenSeen(path string) bool {
	key := normalizePath(path)
	t.mu.RLock()
	_, ok := t.modtimes[key]
	t.mu.RUnlock()
	return ok
}

// SnapshotTracked returns a snapshot of all tracked paths and their last-known
// mtimes. Used by run_command to detect which files the agent previously read
// were modified by an external command (e.g. gofmt, sed -i, git checkout, or a
// code generator).
func (t *FileIntegrityTracker) SnapshotTracked() map[string]time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]time.Time, len(t.modtimes))
	for k, v := range t.modtimes {
		out[k] = v
	}
	return out
}

// ChangedSince returns the paths whose current mtime is newer than the
// recorded mtime in the snapshot. This detects files that were modified on
// disk between the snapshot and now — typically by a command the agent ran.
// Files that no longer exist are excluded.
func (t *FileIntegrityTracker) ChangedSince(snapshot map[string]time.Time) []string {
	var changed []string
	for path, oldMtime := range snapshot {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(oldMtime) {
			changed = append(changed, path)
			// Update the tracker so subsequent stale-read checks are relative
			// to this external change, not the agent's prior read.
			t.mu.Lock()
			t.modtimes[path] = info.ModTime()
			t.mu.Unlock()
		}
	}
	return changed
}

// Reset removes all tracked mtimes. Primarily for testing.
func (t *FileIntegrityTracker) Reset() {
	t.mu.Lock()
	t.modtimes = make(map[string]time.Time)
	t.mu.Unlock()
}

// detectChangedFilesFromCommand returns a notice string listing any tracked
// files whose mtime changed during command execution. This gives the agent
// proactive feedback about which files were modified by a command (e.g.
// gofmt, sed -i, git checkout, protoc) so it knows to re-read them before
// editing, without waiting for a stale-read error on the next edit_file call.
//
// The snapshot is taken BEFORE the command runs (by SnapshotTracked). Returns
// an empty string if no tracked files changed.
func detectChangedFilesFromCommand(snapshot map[string]time.Time) string {
	changed := defaultFileTracker.ChangedSince(snapshot)
	if len(changed) == 0 {
		return ""
	}
	sort.Strings(changed)
	// Limit to 20 files to avoid flooding the output for commands that touch
	// many files (e.g. gofmt .).
	shown := changed
	more := ""
	if len(shown) > 20 {
		shown = shown[:20]
		more = fmt.Sprintf(" (+%d more)", len(changed)-20)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n[files changed by command] %d file(s) modified on disk%s - re-read before editing:", len(changed), more))
	for _, p := range shown {
		sb.WriteString("\n  " + p)
	}
	return sb.String()
}

// staleReadHint checks whether the file at path was modified externally since
// the agent's last read or write. If stale, it returns a diagnostic string
// suitable for appending to an edit tool's error message. If not stale (or
// never tracked), it returns an empty string.
//
// This is used by edit_file, multi_edit_file, and multi_file_edit to provide
// actionable context when an old_text match fails: instead of a bare
// "old_text not found", the agent learns that the file changed externally and
// should be re-read. This saves multiple wasted retry cycles in multi-agent
// scenarios (spawn_agent, swarm teammates) and after git operations that
// modify the working tree.
func staleReadHint(path string) string {
	stale, since := defaultFileTracker.CheckStale(path)
	if !stale {
		return ""
	}
	return fmt.Sprintf("file was modified externally since last read (changed after %s) — re-read with read_file to get current content", since.Format("2006-01-02 15:04:05"))
}

// blindWriteWarning checks whether the agent is about to overwrite a file it
// has never read in the current session. If so, it returns a warning string
// suitable for appending to write_file and multi_file_write output.
//
// This catches a common data-loss scenario: the agent uses write_file to
// replace a file's contents without first reading it, destroying existing
// code it never saw. Unlike edit_file (which requires old_text to match the
// current content), write_file performs no such verification — this warning
// fills that gap by making the model aware it is doing a blind overwrite.
//
// Returns an empty string for new files (no existing content to lose) or
// files the agent has already interacted with.
func blindWriteWarning(path string) string {
	if !defaultFileTracker.HasBeenSeen(path) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return fmt.Sprintf(
				"\nWarning: overwriting %s (%d bytes) without having read it in this session. "+
					"The previous content will be lost. Consider using read_file first, or edit_file for targeted changes.",
				path, info.Size(),
			)
		}
	}
	return ""
}
