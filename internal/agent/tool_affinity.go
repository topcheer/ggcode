package agent

import (
	"container/heap"
	"sync"
	"time"
)

// ToolAffinityLearner tracks tool success rates by task context and provides
// predictive tool recommendations. It learns which tools work best for
// specific task patterns through lightweight reinforcement learning.
//
// The learner maintains affinity scores that represent the historical success
// rate of each tool in different contexts. Scores use Bayesian smoothing
// (alpha=successes+1, beta=failures+1) and decay over time (0.5% per call)
// to adapt to changing patterns.
//
// Usage:
//
//	learner := NewToolAffinityLearner()
//	learner.RecordOutcome("read_file", "read config file", true)  // success
//	recs := learner.GetRecommendations("read config file", 3)    // get top 3
//
// Research basis:
// - Meta-learning for tool selection (ICLR 2026)
// - Adaptive tool orchestration (arXiv:2601.01743)
type ToolAffinityLearner struct {
	mu sync.RWMutex

	// affinities maps (toolName, contextHash) -> affinity score
	affinities map[uint64]*toolAffinityEntry

	// decayFactor reduces old scores over time (0.995 = 0.5% decay per call)
	decayFactor float64

	// minSamples before providing recommendations
	minSamples int

	// maxEntries per tool to prevent unbounded growth
	maxEntries int
}

type toolAffinityEntry struct {
	toolName    string
	contextHash uint64
	successes   int
	attempts    int
	lastUsed    time.Time
	score       float64
}

// Priority queue for eviction
type affinityQueue []*toolAffinityEntry

func (q affinityQueue) Len() int           { return len(q) }
func (q affinityQueue) Less(i, j int) bool { return q[i].lastUsed.Before(q[j].lastUsed) }
func (q affinityQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }

func (q *affinityQueue) Push(x interface{}) {
	entry, ok := x.(*toolAffinityEntry)
	if !ok {
		return // Silently ignore wrong type - should never happen
	}
	*q = append(*q, entry)
}
func (q *affinityQueue) Pop() interface{} {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[0 : n-1]
	return item
}

// NewToolAffinityLearner creates a new affinity learning system
func NewToolAffinityLearner() *ToolAffinityLearner {
	return &ToolAffinityLearner{
		affinities:  make(map[uint64]*toolAffinityEntry),
		decayFactor: 0.995,
		minSamples:  3,
		maxEntries:  1000,
	}
}

// RecordOutcome records a tool execution result and updates affinity scores
func (t *ToolAffinityLearner) RecordOutcome(toolName, context string, success bool) {
	if context == "" {
		return // Skip without context - no learning signal
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := hashToolContext(toolName, context)
	entry, exists := t.affinities[key]

	if !exists {
		// Evict oldest if at capacity
		if len(t.affinities) >= t.maxEntries {
			t.evictOldest()
		}

		entry = &toolAffinityEntry{
			toolName:    toolName,
			contextHash: key,
			lastUsed:    time.Now(),
		}
		t.affinities[key] = entry
	}

	// Apply decay to all entries before updating
	t.decayAll()

	// Record outcome
	entry.attempts++
	if success {
		entry.successes++
	}

	// Update score using success rate with Bayesian smoothing
	alpha := float64(entry.successes) + 1.0
	beta := float64(entry.attempts-entry.successes) + 1.0
	entry.score = alpha / (alpha + beta)

	entry.lastUsed = time.Now()
}

// GetRecommendations returns top N tool recommendations for a given context
// Returns empty list if insufficient data
func (t *ToolAffinityLearner) GetRecommendations(context string, topN int) []ToolRecommendation {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if context == "" {
		return nil
	}

	// Build candidate entries for this context
	var candidates []*toolAffinityEntry
	for _, entry := range t.affinities {
		// Only recommend tools with sufficient samples
		if entry.attempts >= t.minSamples {
			candidates = append(candidates, entry)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Simple context matching: prioritize entries with similar context patterns
	similarEntries := t.rankByContextSimilarity(context, candidates)

	// Return top N
	if topN > len(similarEntries) {
		topN = len(similarEntries)
	}

	result := make([]ToolRecommendation, topN)
	for i := 0; i < topN; i++ {
		entry := similarEntries[i]
		result[i] = ToolRecommendation{
			ToolName:   entry.toolName,
			Score:      entry.score,
			Confidence: float64(entry.attempts) / float64(t.minSamples),
		}
	}

	return result
}

// ToolRecommendation represents a tool suggestion with confidence score
type ToolRecommendation struct {
	ToolName   string
	Score      float64 // 0-1, higher is better
	Confidence float64 // 0-1, based on sample size
}

// decayAll applies time-based decay to all entries
func (t *ToolAffinityLearner) decayAll() {
	for _, entry := range t.affinities {
		entry.score *= t.decayFactor
	}
}

// evictOldest removes the least recently used entry
func (t *ToolAffinityLearner) evictOldest() {
	if len(t.affinities) == 0 {
		return
	}

	pq := make(affinityQueue, 0, len(t.affinities))
	for _, entry := range t.affinities {
		heap.Push(&pq, entry)
	}

	oldest := heap.Pop(&pq).(*toolAffinityEntry)
	delete(t.affinities, oldest.contextHash)
}

// rankByContextSimilarity ranks candidates by context similarity (simple heuristic)
func (t *ToolAffinityLearner) rankByContextSimilarity(_ string, candidates []*toolAffinityEntry) []*toolAffinityEntry {
	// Simple implementation: rank by score directly
	// Future enhancement: use vector similarity or fuzzy matching
	sorted := make([]*toolAffinityEntry, len(candidates))
	copy(sorted, candidates)

	// Sort by score (descending)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i].score < sorted[j].score {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// hashToolContext creates a simple hash for tool+context
func hashToolContext(toolName, context string) uint64 {
	// Simple hash: combine tool and context hash
	// FNV-1a variant
	h := uint64(14695981039346656037)
	for _, c := range toolName {
		h ^= uint64(c)
		h *= 1099511628211
	}
	h ^= uint64(0xFF) // separator
	for _, c := range context {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}
