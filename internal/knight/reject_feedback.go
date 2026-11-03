package knight

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

type rejectFeedbackEntry struct {
	Time     time.Time `json:"time"`
	Name     string    `json:"name"`
	Scope    string    `json:"scope,omitempty"`
	Action   string    `json:"action"`
	Reason   string    `json:"reason,omitempty"`
	Reporter string    `json:"reporter,omitempty"`
}

type rejectFeedbackStore struct {
	mu      sync.Mutex
	path    string
	entries []rejectFeedbackEntry
	loaded  bool
}

const rejectCoolDownWindow = 7 * 24 * time.Hour

const maxRejectFeedbackEntries = 500

func newRejectFeedbackStore(path string) *rejectFeedbackStore {
	return &rejectFeedbackStore{path: path}
}

func normalizeRejectEntryScope(entry *rejectFeedbackEntry) {
	entry.Scope = strings.ToLower(strings.TrimSpace(entry.Scope))
	if entry.Scope == "" {
		entry.Scope = "project"
	}
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

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var entry rejectFeedbackEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			debug.Log("knight", "reject feedback %s: skipping corrupt line: %v", filepath.Base(s.path), err)
			continue
		}
		normalizeRejectEntryScope(&entry)
		s.entries = append(s.entries, entry)
	}
	if err := sc.Err(); err != nil {
		debug.Log("knight", "reject feedback %s: scan error: %v", filepath.Base(s.path), err)
	}
	s.trimOld(time.Time{})
}

func (s *rejectFeedbackStore) Append(entry rejectFeedbackEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}

	normalizeRejectEntryScope(&entry)
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

	if len(s.entries) > maxRejectFeedbackEntries {
		sort.SliceStable(s.entries, func(i, j int) bool { return s.entries[i].Time.After(s.entries[j].Time) })
		s.entries = s.entries[:maxRejectFeedbackEntries]
	}
}

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
		if scope != "" && e.Scope != scope {
			continue
		}
		if !found || e.Time.After(latest.Time) {
			latest = e
			found = true
		}
	}
	return latest, found
}

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

func (k *Knight) ClearRejectFeedback() error {
	if k == nil || k.rejects == nil {
		return nil
	}
	return k.rejects.Clear()
}
