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
	t.mu.Unlock()
}

func (t *FileIntegrityTracker) Reset() {
	t.mu.Lock()
	t.modtimes = make(map[string]time.Time)
	t.mu.Unlock()
}

func detectChangedFilesFromCommand(snapshot map[string]time.Time) string {
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
