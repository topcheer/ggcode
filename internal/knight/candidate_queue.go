package knight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
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
	cleanName := sanitizeCandidateName(candidate.Name)
	if !candidateNameAcceptable(cleanName) {
		debug.Log("knight", "candidate dropped: name %q not acceptable after sanitize -> %q", candidate.Name, cleanName)
		return nil
	}
	candidate.Name = cleanName
	now := time.Now()
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
		items[i] = mergeQueuedCandidate(items[i], candidate, now)
		replaced = true
		break
	}
	if !replaced {
		candidate = initializeQueuedCandidate(candidate, now)
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
	add := func(c SkillCandidate) {
		clean := sanitizeCandidateName(c.Name)
		if !candidateNameAcceptable(clean) {
			debug.Log("knight", "merge skipped candidate %q (sanitized %q rejected)", c.Name, clean)
			return
		}
		c.Name = clean
		key := formatSkillRef(c.Scope, c.Name)
		if existing, ok := merged[key]; ok {
			merged[key] = mergeCandidateSignals(existing, c)
			return
		}
		merged[key] = c
	}
	for _, candidate := range queued {
		add(candidate)
	}
	for _, candidate := range fresh {
		add(candidate)
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

func mergeQueuedCandidate(existing, incoming SkillCandidate, now time.Time) SkillCandidate {
	merged := mergeCandidateSignals(existing, incoming)
	firstSeen := earliestNonZero(existing.FirstQueuedAt, incoming.FirstQueuedAt)
	if firstSeen.IsZero() {
		firstSeen = now
	}
	merged.FirstQueuedAt = firstSeen
	merged.LastQueuedAt = now
	merged.QueueTouchCount = maxInt(existing.QueueTouchCount, incoming.QueueTouchCount)
	if merged.QueueTouchCount == 0 {
		merged.QueueTouchCount = 1
	}
	merged.QueueTouchCount++
	return merged
}

func initializeQueuedCandidate(candidate SkillCandidate, now time.Time) SkillCandidate {
	if candidate.FirstQueuedAt.IsZero() {
		candidate.FirstQueuedAt = now
	}
	if candidate.LastQueuedAt.IsZero() {
		candidate.LastQueuedAt = now
	}
	if candidate.QueueTouchCount == 0 {
		candidate.QueueTouchCount = 1
	}
	candidate.SourceSessions = uniqueSortedStrings(candidate.SourceSessions)
	return candidate
}

func mergeCandidateSignals(existing, incoming SkillCandidate) SkillCandidate {
	preferred := pickPreferredCandidate(existing, incoming)
	preferred.SourceSessions = uniqueSortedStrings(append(append([]string(nil), existing.SourceSessions...), incoming.SourceSessions...))
	if len(preferred.SourceSessions) > preferred.EvidenceCount {
		preferred.EvidenceCount = len(preferred.SourceSessions)
	}
	if len(incoming.Evidence) > len(existing.Evidence) {
		preferred.Evidence = append([]string(nil), incoming.Evidence...)
	} else {
		preferred.Evidence = append([]string(nil), existing.Evidence...)
	}
	if strings.TrimSpace(preferred.Reason) == "" {
		if strings.TrimSpace(incoming.Reason) != "" {
			preferred.Reason = incoming.Reason
		} else {
			preferred.Reason = existing.Reason
		}
	}
	preferred.FirstQueuedAt = earliestNonZero(existing.FirstQueuedAt, incoming.FirstQueuedAt)
	preferred.LastQueuedAt = latestNonZero(existing.LastQueuedAt, incoming.LastQueuedAt)
	preferred.QueueTouchCount = maxInt(existing.QueueTouchCount, incoming.QueueTouchCount)
	return preferred
}

func sortSkillCandidates(candidates []SkillCandidate) {
	sortSkillCandidatesAt(candidates, time.Now())
}

func sortSkillCandidatesAt(candidates []SkillCandidate, now time.Time) {
	for i := range candidates {
		priority, reason := candidateQueuePriority(candidates[i], now)
		candidates[i].QueuePriority = priority
		candidates[i].QueuePriorityReason = reason
	}
	remaining := append([]SkillCandidate(nil), candidates...)
	selected := make([]SkillCandidate, 0, len(candidates))
	seenCategory := map[string]int{}
	seenScope := map[string]int{}
	lastCategory := ""
	lastScope := ""
	for len(remaining) > 0 {
		bestIdx := 0
		bestScore := diversityAdjustedPriority(remaining[0], seenCategory, seenScope, lastCategory, lastScope)
		for i := 1; i < len(remaining); i++ {
			score := diversityAdjustedPriority(remaining[i], seenCategory, seenScope, lastCategory, lastScope)
			if score > bestScore || (score == bestScore && candidateOrderLess(remaining[i], remaining[bestIdx])) {
				bestIdx = i
				bestScore = score
			}
		}
		picked := remaining[bestIdx]
		selected = append(selected, picked)
		seenCategory[normalizeCandidateCategory(picked.Category)]++
		seenScope[strings.TrimSpace(picked.Scope)]++
		lastCategory = normalizeCandidateCategory(picked.Category)
		lastScope = strings.TrimSpace(picked.Scope)
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	copy(candidates, selected)
}

func candidateQueuePriority(candidate SkillCandidate, now time.Time) (float64, string) {
	priority := float64(candidate.EvidenceCount)*100 + candidate.Score*20
	reasons := []string{
		"evidence",
		"score",
	}
	if !candidate.FirstQueuedAt.IsZero() {
		ageDays := int(now.Sub(candidate.FirstQueuedAt).Hours() / 24)
		if ageDays > 0 {
			if ageDays > 21 {
				ageDays = 21
			}
			priority += float64(ageDays * 4)
			reasons = append(reasons, "age")
		}
	}
	if candidate.QueueTouchCount > 0 {
		bonus := candidate.QueueTouchCount
		if bonus > 10 {
			bonus = 10
		}
		priority += float64(bonus * 3)
		reasons = append(reasons, "persistence")
	}
	if n := len(uniqueSortedStrings(candidate.SourceSessions)); n > 0 {
		if n > 5 {
			n = 5
		}
		priority += float64(n * 5)
		reasons = append(reasons, "novelty")
	}
	if candidate.GenFailCount > 0 {
		priority -= float64(candidate.GenFailCount * 25)
		reasons = append(reasons, "gen-fail-penalty")
	}
	return priority, strings.Join(reasons, ", ")
}

func diversityAdjustedPriority(candidate SkillCandidate, seenCategory, seenScope map[string]int, lastCategory, lastScope string) float64 {
	score := candidate.QueuePriority
	category := normalizeCandidateCategory(candidate.Category)
	scope := strings.TrimSpace(candidate.Scope)
	if seenCategory[category] == 0 {
		score += 12
	} else {
		score += 4.0 / float64(seenCategory[category]+1)
	}
	if seenScope[scope] == 0 {
		score += 6
	}
	if category != "" && category != lastCategory {
		score += 2
	}
	if scope != "" && scope != lastScope {
		score += 1
	}
	return score
}

func normalizeCandidateCategory(category string) string {
	category = strings.TrimSpace(strings.ToLower(category))
	if category == "" {
		return "unknown"
	}
	return category
}

func candidateOrderLess(a, b SkillCandidate) bool {
	if a.QueuePriority != b.QueuePriority {
		return a.QueuePriority > b.QueuePriority
	}
	if !a.FirstQueuedAt.Equal(b.FirstQueuedAt) {
		if a.FirstQueuedAt.IsZero() {
			return false
		}
		if b.FirstQueuedAt.IsZero() {
			return true
		}
		return a.FirstQueuedAt.Before(b.FirstQueuedAt)
	}
	if a.EvidenceCount != b.EvidenceCount {
		return a.EvidenceCount > b.EvidenceCount
	}
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.Name < b.Name
}

func earliestNonZero(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.Before(b) {
		return a
	}
	return b
}

func latestNonZero(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.After(b) {
		return a
	}
	return b
}

func uniqueSortedStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
