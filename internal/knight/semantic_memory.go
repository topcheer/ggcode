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
	// #r25 (ICML 2026 Experience-Driven Self-Distillation): principle-level
	// memories carry an empirical utility score. Repeated recurrence of the
	// same lesson MERGES into one entry (semantic dedup) instead of flooding
	// the store; eviction prunes by utility, not blind FIFO. Older JSONL
	// records without these fields unmarshal as zero values and map to the
	// 0.5 default via entryUtility below - no migration needed.
	Utility     float64   `json:"utility,omitempty"` // [0,1]; 0 = unset (default 0.5)
	Hits        int       `json:"hits,omitempty"`    // times this lesson recurred / was injected
	LastApplied time.Time `json:"last_applied,omitempty"`
}

// entryUtility returns the effective utility of an entry, defaulting
// legacy zero-value records to 0.5 so they neither dominate nor get
// insta-pruned relative to fresh entries.
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

	// #982: single lock (pathMu) — see semanticMemoryPathMu comment.
	pathMu := semanticMemoryPathLock(s.path)
	pathMu.Lock()
	defer pathMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	// #3029: pathMu only serializes writers within THIS process. The storage
	// path is per-workspace shared (daemon/TUI/A2A multi-instance is the
	// norm here), so two processes doing read-modify-write on the same file
	// silently dropped the first writer's entry (atomic rename prevents
	// tearing, not loss - see the same incident class in
	// appendSkillScenario). Guard the whole read-modify-write with the
	// cross-process file lock; degrade gracefully when the lock cannot be
	// acquired (same contract as auth/store.go, playbook.go).
	if unlock, err := util.FileLock(s.path + ".lock"); err == nil {
		defer unlock()
	}
	entries, readErr := readSemanticMemoryEntries(s.path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		// #769: on a real read failure (IO error, bufio.ErrTooLong) the old
		// code proceeded with nil and the rewrite below wiped all history.
		return fmt.Errorf("semantic memory: read %s: %w", s.path, readErr)
	}
	// #r25: semantic dedup - if an existing entry says substantially the
	// same thing (jaccard over the similarity tokenizer already used for
	// skill dedup), MERGE instead of appending. A lesson that keeps
	// recurring is one strong principle, not N rows flooding the 500-slot
	// store (which starved heterogeneous lessons under FIFO eviction).
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
		// #r25: utility-aware eviction replaces blind FIFO. Lowest effective
		// utility goes first; ties break to oldest.
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

// semanticDedupScanWindow bounds the dedup scan to the most recent N
// entries (cost control; near-duplicates cluster in time).
const semanticDedupScanWindow = 200

// TopByUtility returns at most limit entries ranked by effective utility
// (desc), breaking ties by recency. #r25: the eval-prompt injection path
// uses this instead of plain recency so lessons that repeatedly proved
// useful outrank whatever happened most recently (ICML 2026 top-ranked
// principles).
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

// Reinforce adjusts an entry's utility by delta (positive or negative,
// clamped to [0,1]) and records the application timestamp. #r25 feedback
// loop: eval decisions that acted on injected lessons feed the score back.
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

// TopSemanticMemoryByUtility returns entries ranked by empirical utility
// (#r25): lessons that repeatedly proved useful outrank recent-but-unproven
// ones. Used by the eval-prompt injection path.
func (k *Knight) TopSemanticMemoryByUtility(limit int) ([]SemanticMemoryEntry, error) {
	if k == nil {
		return nil, nil
	}
	return newSemanticMemoryStore(k.semanticMemoryPath()).TopByUtility(limit)
}

// ReinforceSemanticMemory adjusts a lesson's utility score after an eval
// decision acted on it (#r25 feedback loop). Safe on nil Knight.
func (k *Knight) ReinforceSemanticMemory(id string, delta float64) error {
	if k == nil {
		return nil
	}
	return newSemanticMemoryStore(k.semanticMemoryPath()).Reinforce(id, delta)
}
