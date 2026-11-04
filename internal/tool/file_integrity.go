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

type FileIntegrityTracker struct {
	mu       sync.RWMutex
	modtimes map[string]time.Time
	// vacated records tracked paths that disappeared during a run_command
	// (external mv/rm), keyed by normalized path. Writes to such paths are
	// refused until the vacancy is acknowledged via read_file (#5026,
	// #5019 ruling option 2). Stat-gated lookups keep externally recreated
	// paths frictionless.
	vacated map[string]struct{}
}

var defaultFileTracker = NewFileIntegrityTracker()

var writePathLocks sync.Map

func LockWritePath(path string) func() {
	v, _ := writePathLocks.LoadOrStore(normalizePath(path), &sync.Mutex{})
	mu, ok := v.(*sync.Mutex)
	if !ok {
		return func() {}
	}
	mu.Lock()
	return mu.Unlock
}

func NewFileIntegrityTracker() *FileIntegrityTracker {
	return &FileIntegrityTracker{
		modtimes: make(map[string]time.Time),
		vacated:  make(map[string]struct{}),
	}
}

func normalizePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	abs = filepath.Clean(abs)

	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	// The file itself may be missing (stat-gated lookups, vacated paths): a
	// bare abs fallback resolves differently than when the file existed on
	// macOS (/var -> /private/var tmpdirs), breaking key equality between
	// record-time and check-time. Resolve the parent directory instead so a
	// vanished file keeps the key it had while it existed (#5026).
	if resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(resolvedDir, filepath.Base(abs))
	}
	return abs
}

func (t *FileIntegrityTracker) RecordRead(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	key := normalizePath(path)
	t.mu.Lock()
	t.modtimes[key] = info.ModTime()
	// A read that finds the file on disk supersedes any vacated marker
	// (e.g. the file was recreated externally, or the read is the ack of a
	// move hint and the path exists again).
	delete(t.vacated, key)
	t.mu.Unlock()
}

func (t *FileIntegrityTracker) RecordWrite(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	key := normalizePath(path)
	t.mu.Lock()
	t.modtimes[key] = info.ModTime()
	// The write gate only refuses paths that are still missing; once the
	// file exists again the vacancy is stale and must not block future runs.
	delete(t.vacated, key)
	t.mu.Unlock()
}

func (t *FileIntegrityTracker) CheckStale(path string) (stale bool, since time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return false, time.Time{}
	}
	key := normalizePath(path)
	t.mu.RLock()
	lastKnown, ok := t.modtimes[key]
	t.mu.RUnlock()
	if !ok {
		return false, time.Time{}
	}
	current := info.ModTime()
	if current.After(lastKnown) {
		return true, lastKnown
	}
	return false, time.Time{}
}

func (t *FileIntegrityTracker) HasBeenSeen(path string) bool {
	key := normalizePath(path)
	t.mu.RLock()
	_, ok := t.modtimes[key]
	t.mu.RUnlock()
	return ok
}

func (t *FileIntegrityTracker) SnapshotTracked() map[string]time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]time.Time, len(t.modtimes))
	for k, v := range t.modtimes {
		out[k] = v
	}
	return out
}

func (t *FileIntegrityTracker) ChangedSince(snapshot map[string]time.Time) []string {
	var changed []string
	for path, oldMtime := range snapshot {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(oldMtime) {
			changed = append(changed, path)

			t.mu.Lock()
			t.modtimes[path] = info.ModTime()
			t.mu.Unlock()
		}
	}
	return changed
}

func (t *FileIntegrityTracker) RemoveTracking(path string) {
	key := normalizePath(path)
	t.mu.Lock()
	delete(t.modtimes, key)
	delete(t.vacated, key)
	t.mu.Unlock()
}

func (t *FileIntegrityTracker) Reset() {
	t.mu.Lock()
	t.modtimes = make(map[string]time.Time)
	t.vacated = make(map[string]struct{})
	t.mu.Unlock()
}

// RecordVacated marks the given normalized snapshot keys as vacated after a
// run_command where os.Stat now fails for them (external mv/rm). Only keys
// that were tracked before the command are considered, keeping the gate
// deterministic (#5026).
func (t *FileIntegrityTracker) RecordVacated(snapshot map[string]time.Time) {
	var vacated []string
	for path := range snapshot {
		if _, err := os.Stat(path); err != nil {
			vacated = append(vacated, path)
		}
	}
	if len(vacated) == 0 {
		return
	}
	t.mu.Lock()
	for _, path := range vacated {
		t.vacated[path] = struct{}{}
	}
	t.mu.Unlock()
}

// VacancyCheck reports whether path is currently vacated: marked during this
// session AND still missing on disk. Externally recreated paths (stat
// succeeds) are not refused — normal stale/blind rules apply to them.
func (t *FileIntegrityTracker) VacancyCheck(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return false
	}
	key := normalizePath(path)
	t.mu.RLock()
	_, ok := t.vacated[key]
	t.mu.RUnlock()
	return ok
}

// ClearVacated drops the vacated marker for path (ack route: read_file hits
// the not-found+hint and acknowledges the move, so a subsequent intentional
// write proceeds as fresh creation).
func (t *FileIntegrityTracker) ClearVacated(path string) {
	key := normalizePath(path)
	t.mu.Lock()
	delete(t.vacated, key)
	t.mu.Unlock()
}

func detectChangedFilesFromCommand(snapshot map[string]time.Time) string {
	// Vacancy recording is deterministic and stat-based: any pre-command
	// tracked key that no longer exists afterwards was vacated by the
	// command (mv/rm) — the write gate refuses it until acknowledged
	// (#5026). rm coverage comes free; file_ops moves migrate tracking
	// themselves and never reach here.
	defaultFileTracker.RecordVacated(snapshot)
	changed := defaultFileTracker.ChangedSince(snapshot)
	if len(changed) == 0 {
		return ""
	}
	sort.Strings(changed)

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

func staleReadHint(path string) string {
	stale, since := defaultFileTracker.CheckStale(path)
	if !stale {
		return ""
	}
	return fmt.Sprintf("file was modified externally since last read (changed after %s) — re-read with read_file to get current content", since.Format("2006-01-02 15:04:05"))
}

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
