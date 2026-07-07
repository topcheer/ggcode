package knight

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// rejectFeedbackEntry records a single user (or scheduler) decision to drop a
// candidate skill. We persist enough context that the next analysis pass can
// avoid regenerating the same idea immediately.
type rejectFeedbackEntry struct {
	Time     time.Time `json:"time"`
	Name     string    `json:"name"`
	Scope    string    `json:"scope,omitempty"`
	Action   string    `json:"action"` // "reject", "rollback", "auto-reject"
	Reason   string    `json:"reason,omitempty"`
	Reporter string    `json:"reporter,omitempty"` // "user", "validator", "scheduler"
}

// rejectFeedbackStore is a tiny append-only JSONL store with an in-memory
// lookup index keyed by lowercased skill name + scope.
type rejectFeedbackStore struct {
	mu      sync.Mutex
	path    string
	entries []rejectFeedbackEntry
	loaded  bool
}

// rejectCoolDownWindow is how long a same-name candidate is suppressed after a
// recent reject/rollback. Long enough that an analyzer doesn't replay the same
// idea on the next tick, short enough that a genuinely useful skill can resurface.
const rejectCoolDownWindow = 7 * 24 * time.Hour

// maxRejectFeedbackEntries caps in-memory entries to prevent unbounded growth
// from long-running sessions with many reject cycles.
const maxRejectFeedbackEntries = 500

func newRejectFeedbackStore(path string) *rejectFeedbackStore {
	return &rejectFeedbackStore{path: path}
}

func (s *rejectFeedbackStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	f, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for dec.More() {
		var entry rejectFeedbackEntry
		if err := dec.Decode(&entry); err != nil {
			return
		}
		s.entries = append(s.entries, entry)
	}
	s.trimOld(time.Time{}) // trim stale entries on load
}

// Append persists a new feedback entry and returns the stored copy.
func (s *rejectFeedbackStore) Append(entry rejectFeedbackEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	s.trimOld(entry.Time)
	return nil
}

// trimOld removes entries that are beyond the cool-down window (no longer
// useful for lookups) and caps the in-memory slice at maxRejectFeedbackEntries.
// This prevents unbounded growth from long-running sessions. The on-disk file
// is not rewritten — only the in-memory index is trimmed. Old entries in the
// file are harmless (they fall outside the cool-down window and are naturally
// ignored by LastFor/coolDownActive).
func (s *rejectFeedbackStore) trimOld(now time.Time) {
	if len(s.entries) == 0 {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-rejectCoolDownWindow)
	trimmed := s.entries[:0]
	for _, e := range s.entries {
		if e.Time.After(cutoff) {
			trimmed = append(trimmed, e)
		}
	}
	s.entries = trimmed
	// Hard cap: keep only the most recent N entries.
	if len(s.entries) > maxRejectFeedbackEntries {
		sort.SliceStable(s.entries, func(i, j int) bool { return s.entries[i].Time.After(s.entries[j].Time) })
		s.entries = s.entries[:maxRejectFeedbackEntries]
	}
}

// LastFor returns the most recent feedback entry for a (scope, name) pair, if
// any. Lookup is case-insensitive on name; scope must match exactly when
// non-empty.
func (s *rejectFeedbackStore) LastFor(scope, name string) (rejectFeedbackEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	name = strings.ToLower(strings.TrimSpace(name))
	scope = strings.TrimSpace(scope)
	var latest rejectFeedbackEntry
	found := false
	for _, e := range s.entries {
		if strings.ToLower(strings.TrimSpace(e.Name)) != name {
			continue
		}
		if scope != "" && e.Scope != "" && e.Scope != scope {
			continue
		}
		if !found || e.Time.After(latest.Time) {
			latest = e
			found = true
		}
	}
	return latest, found
}

// Recent returns the most recent N entries newest-first.
func (s *rejectFeedbackStore) Recent(limit int) []rejectFeedbackEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	out := append([]rejectFeedbackEntry(nil), s.entries...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Clear truncates the underlying log; used by tests and `/knight reject-history clear`.
func (s *rejectFeedbackStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = util.AtomicWriteFile(s.path, []byte{}, 0600)
	return nil
}

// rejectCoolDownActive reports whether a candidate is currently suppressed by
// a recent reject/rollback. Returns the entry that caused the cool-down for
// human messaging.
func (s *rejectFeedbackStore) coolDownActive(scope, name string, now time.Time) (rejectFeedbackEntry, bool) {
	last, ok := s.LastFor(scope, name)
	if !ok {
		return rejectFeedbackEntry{}, false
	}
	if now.Sub(last.Time) >= rejectCoolDownWindow {
		return rejectFeedbackEntry{}, false
	}
	return last, true
}

// formatRejectFeedback renders a stored entry for status output.
func formatRejectFeedback(entry rejectFeedbackEntry) string {
	when := ""
	if !entry.Time.IsZero() {
		when = entry.Time.Format("2006-01-02 15:04") + " "
	}
	scope := entry.Scope
	if scope == "" {
		scope = "?"
	}
	reason := strings.TrimSpace(entry.Reason)
	if reason != "" {
		reason = " — " + reason
	}
	reporter := entry.Reporter
	if reporter == "" {
		reporter = "user"
	}
	return fmt.Sprintf("%s%s:%s [%s by %s]%s", when, scope, entry.Name, entry.Action, reporter, reason)
}

// --- Knight surface ---------------------------------------------------------

// RecentRejectFeedback returns at most limit recent reject/rollback entries
// (newest first) formatted as human-readable strings.
func (k *Knight) RecentRejectFeedback(limit int) []string {
	if k == nil || k.rejects == nil {
		return nil
	}
	entries := k.rejects.Recent(limit)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, formatRejectFeedback(e))
	}
	return out
}

// ClearRejectFeedback wipes the reject feedback log; rejected candidates may
// then be re-staged on the next analysis tick.
func (k *Knight) ClearRejectFeedback() error {
	if k == nil || k.rejects == nil {
		return nil
	}
	return k.rejects.Clear()
}
