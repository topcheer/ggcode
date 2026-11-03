package knight

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

type SemanticMemoryEntry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Kind      string    `json:"kind"`
	Summary   string    `json:"summary"`
	Refs      []string  `json:"refs,omitempty"`
	Source    string    `json:"source,omitempty"`
	SessionID string    `json:"session,omitempty"`

	Utility     float64   `json:"utility,omitempty"`
	Hits        int       `json:"hits,omitempty"`
	LastApplied time.Time `json:"last_applied,omitempty"`
}

func entryUtility(e SemanticMemoryEntry) float64 {
	if e.Utility <= 0 {
		return 0.5
	}
	return e.Utility
}

const (
	maxSemanticMemoryEntries = 500
	maxSemanticSummaryRunes  = 1200
)

type semanticMemoryStore struct {
	path string
}

func newSemanticMemoryStore(path string) *semanticMemoryStore {
	return &semanticMemoryStore{path: path}
}

var semanticMemoryPathMu sync.Map

func semanticMemoryPathLock(path string) *sync.Mutex {
	mu, _ := semanticMemoryPathMu.LoadOrStore(path, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

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

	pathMu := semanticMemoryPathLock(s.path)
	pathMu.Lock()
	defer pathMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	if unlock, err := util.FileLock(s.path + ".lock"); err == nil {
		defer unlock()
	}
	entries, readErr := readSemanticMemoryEntries(s.path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {

		return fmt.Errorf("semantic memory: read %s: %w", s.path, readErr)
	}

	newTok := tokenizeForSimilarity(entry.Summary)
	merged := false
	for i := len(entries) - 1; i >= 0 && i >= len(entries)-semanticDedupScanWindow; i-- {
		if jaccardSimilarity(newTok, tokenizeForSimilarity(entries[i].Summary)) >= similarityDuplicateThreshold(newTok) {
			entries[i].Hits++
			entries[i].Utility = math.Min(1, entryUtility(entries[i])+0.1)
			entries[i].Time = entry.Time
			if len(entry.Refs) > 0 {
				entries[i].Refs = append(entries[i].Refs, entry.Refs...)
			}
			merged = true
			break
		}
	}
	if !merged {
		entry.Hits = 1
		entry.Utility = 0.5
		entries = append(entries, entry)
	}
	if len(entries) > maxSemanticMemoryEntries {

		sort.SliceStable(entries, func(a, b int) bool {
			ua, ub := entryUtility(entries[a]), entryUtility(entries[b])
			if ua != ub {
				return ua < ub
			}
			return entries[a].Time.Before(entries[b].Time)
		})
		entries = entries[len(entries)-maxSemanticMemoryEntries:]
		sort.SliceStable(entries, func(a, b int) bool { return entries[a].Time.Before(entries[b].Time) })
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

const semanticDedupScanWindow = 200

func (s *semanticMemoryStore) TopByUtility(limit int) ([]SemanticMemoryEntry, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}
	pathMu := semanticMemoryPathLock(s.path)
	pathMu.Lock()
	defer pathMu.Unlock()
	entries, err := readSemanticMemoryEntries(s.path)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(a, b int) bool {
		ua, ub := entryUtility(entries[a]), entryUtility(entries[b])
		if ua != ub {
			return ua > ub
		}
		return entries[a].Time.After(entries[b].Time)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *semanticMemoryStore) Reinforce(id string, delta float64) error {
	if s == nil || s.path == "" || id == "" {
		return nil
	}
	pathMu := semanticMemoryPathLock(s.path)
	pathMu.Lock()
	defer pathMu.Unlock()
	entries, err := readSemanticMemoryEntries(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].ID == id {
			entries[i].Utility = math.Max(0, math.Min(1, entryUtility(entries[i])+delta))
			entries[i].LastApplied = time.Now().UTC()
			found = true
			break
		}
	}
	if !found {
		return nil
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

func (k *Knight) semanticMemoryPath() string {
	if k == nil {
		return ""
	}
	return filepath.Join(k.projDir, ".ggcode", "knight-memory.jsonl")
}

func (k *Knight) RecordSemanticMemory(kind, summary string, refs []string, source string) error {
	if k == nil {
		return nil
	}
	store := newSemanticMemoryStore(k.semanticMemoryPath())
	return store.Append(SemanticMemoryEntry{Kind: kind, Summary: summary, Refs: refs, Source: source})
}

func (k *Knight) RecentSemanticMemory(limit int) ([]SemanticMemoryEntry, error) {
	if k == nil {
		return nil, nil
	}
	return newSemanticMemoryStore(k.semanticMemoryPath()).Recent(limit)
}

func (k *Knight) TopSemanticMemoryByUtility(limit int) ([]SemanticMemoryEntry, error) {
	if k == nil {
		return nil, nil
	}
	return newSemanticMemoryStore(k.semanticMemoryPath()).TopByUtility(limit)
}

func (k *Knight) ReinforceSemanticMemory(id string, delta float64) error {
	if k == nil {
		return nil
	}
	return newSemanticMemoryStore(k.semanticMemoryPath()).Reinforce(id, delta)
}
