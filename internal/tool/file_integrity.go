package tool

import (
	"fmt"
	"os"
	"path/filepath"
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

// Reset removes all tracked mtimes. Primarily for testing.
func (t *FileIntegrityTracker) Reset() {
	t.mu.Lock()
	t.modtimes = make(map[string]time.Time)
	t.mu.Unlock()
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
