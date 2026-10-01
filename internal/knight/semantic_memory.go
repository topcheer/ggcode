package knight

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// SemanticMemoryEntry is a long-form lesson Knight has accumulated across
// sessions: an approved proposal, a successfully promoted skill, a recurring
// resolved bug pattern, etc. The intent is to give later prompts (and Knight
// itself) cross-session continuity beyond what the active skill set captures.
type SemanticMemoryEntry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Refs      []string  `json:"refs,omitempty"`
	Source    string    `json:"source,omitempty"`
	SessionID string    `json:"session,omitempty"`
}

const (
	maxSemanticMemoryEntries = 500
	maxSemanticSummaryRunes  = 1200
	// semanticMemoryRotateBytes bounds the on-disk jsonl: past this size the
	// write path rotates the file down to the newest maxSemanticMemoryEntries
	// (#3029, mirroring skillScenarioRotateBytes / #1270). With the 500-entry
	// cap and ~1.5KB max summaries, 1.5MB comfortably exceeds the worst-case
	// single-entry size.
	semanticMemoryRotateBytes = 1536 * 1024
)

type semanticMemoryStore struct {
	path string
}

func newSemanticMemoryStore(path string) *semanticMemoryStore {
	return &semanticMemoryStore{path: path}
}

// semanticMemoryPathMu serializes read-modify-write cycles per path across
// store instances. Each call site constructs a fresh store (#769), so a
// per-instance mutex guarded nothing -- concurrent scheduler-goroutine and
// user-command paths could interleave their read/append/rewrite cycles and
// silently drop entries.
//
// #982: the per-instance s.mu was removed entirely. Append used to take
// s.mu then pathMu, while Recent took pathMu then s.mu (via recentLocked) --
// a classic AB-BA inversion between two stores on the same path. The path
// mutex is the single serialization point: it is keyed by the file path, so
// all stores targeting the same JSONL file (the only case where mutual
// exclusion matters) are already serialized by pathMu alone.
var semanticMemoryPathMu sync.Map // path -> *sync.Mutex

func semanticMemoryPathLock(path string) *sync.Mutex {
	mu, _ := semanticMemoryPathMu.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// Append persists a new entry, truncating overly long summaries and capping
// the file at maxSemanticMemoryEntries (oldest entries dropped).
func (s *semanticMemoryStore) Append(entry SemanticMemoryEntry) error {
	if s == nil || s.path == "" {
		return nil
	}
	if entry.Summary = strings.TrimSpace(entry.Summary); entry.Summary == "" {
		return errors.New("semantic memory: empty summary")
	}
	if entry.Kind == "" {
		entry.Kind = "lesson"
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("mem-%d", entry.Time.UnixNano())
	}
	entry.Summary = truncateSanitized(entry.Summary, maxSemanticSummaryRunes)

	// #3029: append a single line instead of read-modify-write rewriting
	// the whole file. The per-path mutex only serializes within this process;
	// daemon/TUI/A2A instances sharing the same workspace previously lost
	// entries to last-writer-wins rewrites (the same failure mode
	// skill_scenario_log fixed with O_APPEND). The entry cap is enforced on
	// read by readSemanticMemoryEntries; write-side rotation bounds the file.
	pathMu := semanticMemoryPathLock(s.path)
	pathMu.Lock()
	defer pathMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Rotation mirrors skill_scenario_log #1270: past the rotate threshold,
	// rewrite the file to the newest window (readSemanticMemoryEntries applies
	// the same cap, so the rewrite is idempotent with the read view). A
	// concurrent appender racing the rewrite can lose its line -- acceptable
	// for an advisory lesson log, bounded to a couple of entries per rotation.
	if info, err := os.Stat(s.path); err == nil && info.Size() > semanticMemoryRotateBytes {
		entries, readErr := readSemanticMemoryEntries(s.path)
		if readErr != nil {
			debug.Log("knight", "semantic memory rotation read failed: %v", readErr)
			return nil
		}
		var b strings.Builder
		for _, e := range entries {
			raw, err := json.Marshal(e)
			if err != nil {
				continue
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		if err := util.AtomicWriteFile(s.path, []byte(b.String()), 0o600); err != nil {
			debug.Log("knight", "semantic memory rotation write failed: %v", err)
		}
	}
	return nil
}

// Recent returns at most limit most-recent entries (newest first).
func (s *semanticMemoryStore) Recent(limit int) ([]SemanticMemoryEntry, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}
	pathMu := semanticMemoryPathLock(s.path)
	pathMu.Lock()
	defer pathMu.Unlock()
	return s.recentLocked(limit)
}

func (s *semanticMemoryStore) recentLocked(limit int) ([]SemanticMemoryEntry, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}
	// #982: no per-instance lock here (caller Recent already holds pathMu).
	entries, err := readSemanticMemoryEntries(s.path)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	out := make([]SemanticMemoryEntry, len(entries))
	for i, e := range entries {
		out[len(entries)-1-i] = e
	}
	return out, nil
}

func readSemanticMemoryEntries(path string) ([]SemanticMemoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []SemanticMemoryEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e SemanticMemoryEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if strings.TrimSpace(e.Summary) == "" {
			continue
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// #3029: the cap now lives on the read side (Append no longer rewrites
	// the file per write). Keep the newest window, chronological order.
	if len(out) > maxSemanticMemoryEntries {
		out = out[len(out)-maxSemanticMemoryEntries:]
	}
	return out, nil
}

// --- Knight surface ---------------------------------------------------------

func (k *Knight) semanticMemoryPath() string {
	if k == nil {
		return ""
	}
	return filepath.Join(k.projDir, ".ggcode", "knight-memory.jsonl")
}

// RecordSemanticMemory persists a cross-session lesson. Safe to call with a
// nil Knight (no-op).
func (k *Knight) RecordSemanticMemory(kind, summary string, refs []string, source string) error {
	if k == nil {
		return nil
	}
	store := newSemanticMemoryStore(k.semanticMemoryPath())
	return store.Append(SemanticMemoryEntry{Kind: kind, Summary: summary, Refs: refs, Source: source})
}

// RecentSemanticMemory returns the most recent semantic memory entries.
func (k *Knight) RecentSemanticMemory(limit int) ([]SemanticMemoryEntry, error) {
	if k == nil {
		return nil, nil
	}
	return newSemanticMemoryStore(k.semanticMemoryPath()).Recent(limit)
}
