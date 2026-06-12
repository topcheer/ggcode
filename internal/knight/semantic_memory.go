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
)

type semanticMemoryStore struct {
	path string
	mu   sync.Mutex
}

func newSemanticMemoryStore(path string) *semanticMemoryStore {
	return &semanticMemoryStore{path: path}
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	entries, _ := readSemanticMemoryEntries(s.path)
	entries = append(entries, entry)
	if len(entries) > maxSemanticMemoryEntries {
		entries = entries[len(entries)-maxSemanticMemoryEntries:]
	}
	var b strings.Builder
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return util.AtomicWriteFile(s.path, []byte(b.String()), 0o600)
}

// Recent returns at most limit most-recent entries (newest first).
func (s *semanticMemoryStore) Recent(limit int) ([]SemanticMemoryEntry, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
