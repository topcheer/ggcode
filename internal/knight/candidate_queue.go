package knight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// CandidateQueue persists deferred high-value skill candidates between runs.
type CandidateQueue struct {
	mu   sync.Mutex
	path string
}

// NewCandidateQueue creates a queue backed by the given JSON file path.
func NewCandidateQueue(path string) *CandidateQueue {
	return &CandidateQueue{path: path}
}

// EnsureDir creates the parent directory for the queue file.
func (q *CandidateQueue) EnsureDir() error {
	return os.MkdirAll(filepath.Dir(q.path), 0o755)
}

// List returns the current deferred candidates sorted by priority.
func (q *CandidateQueue) List() ([]SkillCandidate, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.loadLocked()
	if err != nil {
		return nil, err
	}
	sortSkillCandidates(items)
	return items, nil
}

// Upsert stores or updates a deferred candidate.
func (q *CandidateQueue) Upsert(candidate SkillCandidate) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.loadLocked()
	if err != nil {
		return err
	}
	key := formatSkillRef(candidate.Scope, candidate.Name)
	replaced := false
	for i := range items {
		if formatSkillRef(items[i].Scope, items[i].Name) != key {
			continue
		}
		items[i] = pickPreferredCandidate(items[i], candidate)
		replaced = true
		break
	}
	if !replaced {
		items = append(items, candidate)
	}
	sortSkillCandidates(items)
	return q.saveLocked(items)
}

// Remove deletes a candidate from the queue if present.
func (q *CandidateQueue) Remove(candidate SkillCandidate) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	items, err := q.loadLocked()
	if err != nil {
		return err
	}
	key := formatSkillRef(candidate.Scope, candidate.Name)
	filtered := items[:0]
	for _, item := range items {
		if formatSkillRef(item.Scope, item.Name) == key {
			continue
		}
		filtered = append(filtered, item)
	}
	return q.saveLocked(filtered)
}

func (q *CandidateQueue) loadLocked() ([]SkillCandidate, error) {
	data, err := os.ReadFile(q.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var items []SkillCandidate
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (q *CandidateQueue) saveLocked(items []SkillCandidate) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := q.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, q.path)
}

func mergeSkillCandidates(queued, fresh []SkillCandidate) []SkillCandidate {
	merged := make(map[string]SkillCandidate, len(queued)+len(fresh))
	for _, candidate := range queued {
		merged[formatSkillRef(candidate.Scope, candidate.Name)] = candidate
	}
	for _, candidate := range fresh {
		key := formatSkillRef(candidate.Scope, candidate.Name)
		if existing, ok := merged[key]; ok {
			merged[key] = pickPreferredCandidate(existing, candidate)
			continue
		}
		merged[key] = candidate
	}
	items := make([]SkillCandidate, 0, len(merged))
	for _, candidate := range merged {
		items = append(items, candidate)
	}
	sortSkillCandidates(items)
	return items
}

func pickPreferredCandidate(existing, incoming SkillCandidate) SkillCandidate {
	if incoming.EvidenceCount > existing.EvidenceCount {
		return incoming
	}
	if incoming.EvidenceCount == existing.EvidenceCount && incoming.Score > existing.Score {
		return incoming
	}
	return existing
}

func sortSkillCandidates(candidates []SkillCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].EvidenceCount != candidates[j].EvidenceCount {
			return candidates[i].EvidenceCount > candidates[j].EvidenceCount
		}
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Name < candidates[j].Name
	})
}
