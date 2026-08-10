package agent

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"sync"
	"time"
)

// trajectoryEnhance implements self-improvement through learning from
// successful execution patterns. It extracts high-value tool usage
// patterns from successful trajectories and suggests them in similar
// contexts. This is a lightweight implementation inspired by the
// Self-Improving Agents via Self-Play paradigm (arXiv:2512.02731).
//
// Research basis:
// - GVU (General Value Update) operator for self-improvement
// - Learning from positive trajectory patterns
// - Context-aware pattern matching

const (
	// maxPatternEntries limits memory footprint
	maxPatternEntries = 100
	// minConfidence is the threshold for pattern suggestion
	minConfidence = 0.6
	// patternExpiry is how long a pattern stays active
	patternExpiry = 24 * time.Hour
)

// patternEntry represents a learned tool usage pattern
type patternEntry struct {
	// toolSequence is the ordered list of tools that succeeded
	toolSequence []string
	// contextHash identifies the problem context (task type, file types, etc.)
	contextHash uint32
	// successCount tracks how many times this pattern succeeded
	successCount int
	// attemptCount tracks total attempts
	attemptCount int
	// lastUsed timestamp for expiry
	lastUsed time.Time
	// confidence is successCount / attemptCount
	confidence float64
}

// trajectoryEnhancer manages learned patterns
type trajectoryEnhancer struct {
	mu       sync.RWMutex
	patterns map[uint32]*patternEntry
}

// newTrajectoryEnhancer creates a new enhancer
func newTrajectoryEnhancer() *trajectoryEnhancer {
	return &trajectoryEnhancer{
		patterns: make(map[uint32]*patternEntry),
	}
}

// hashContext creates a deterministic hash from context features
func (te *trajectoryEnhancer) hashContext(task, fileTypes string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(task))
	h.Write([]byte("|"))
	h.Write([]byte(fileTypes))
	return h.Sum32()
}

// extractFileTypes from file paths
func (te *trajectoryEnhancer) extractFileTypes(filePaths []string) string {
	types := make(map[string]bool)
	for _, path := range filePaths {
		ext := te.getExtension(path)
		if ext != "" {
			types[ext] = true
		}
	}
	if len(types) == 0 {
		return "unknown"
	}
	keys := make([]string, 0, len(types))
	for t := range types {
		keys = append(keys, t)
	}
	return strings.Join(keys, ",")
}

func (te *trajectoryEnhancer) getExtension(path string) string {
	if idx := strings.LastIndex(path, "."); idx > 0 {
		return path[idx:]
	}
	return ""
}

// normalizeTask extracts task type from user message
func (te *trajectoryEnhancer) normalizeTask(msg string) string {
	// Remove specific file names, numbers, and quotes
	msg = regexp.MustCompile(`["']\w+\.?\w*["']`).ReplaceAllString(msg, "")
	msg = regexp.MustCompile(`\b\d+\b`).ReplaceAllString(msg, "")
	msg = strings.ToLower(strings.TrimSpace(msg))
	if len(msg) > 100 {
		msg = msg[:100]
	}
	return msg
}

// recordSuccess records a successful trajectory pattern
func (te *trajectoryEnhancer) recordSuccess(task string, toolNames []string, fileTypes string) {
	te.mu.Lock()
	defer te.mu.Unlock()

	ctxHash := te.hashContext(task, fileTypes)
	entry, exists := te.patterns[ctxHash]

	if !exists {
		// Enforce memory limit
		if len(te.patterns) >= maxPatternEntries {
			te.evictOldest()
		}
		entry = &patternEntry{
			toolSequence: make([]string, len(toolNames)),
			contextHash:  ctxHash,
			lastUsed:     time.Now(),
		}
		copy(entry.toolSequence, toolNames)
		te.patterns[ctxHash] = entry
	}

	entry.successCount++
	entry.attemptCount++
	entry.confidence = float64(entry.successCount) / float64(entry.attemptCount)
	entry.lastUsed = time.Now()
}

// evictOldest removes the least recently used pattern
func (te *trajectoryEnhancer) evictOldest() {
	var oldestHash uint32
	var oldestTime time.Time
	for hash, pattern := range te.patterns {
		if oldestTime.IsZero() || pattern.lastUsed.Before(oldestTime) {
			oldestTime = pattern.lastUsed
			oldestHash = hash
		}
	}
	if oldestHash != 0 {
		delete(te.patterns, oldestHash)
	}
}

// suggestPattern suggests a tool sequence based on context
func (te *trajectoryEnhancer) suggestPattern(task string, fileTypes string) (string, []string, bool) {
	te.mu.RLock()
	defer te.mu.RUnlock()

	ctxHash := te.hashContext(task, fileTypes)
	entry, exists := te.patterns[ctxHash]

	if !exists || entry.confidence < minConfidence {
		return "", nil, false
	}

	// Check expiry
	if time.Since(entry.lastUsed) > patternExpiry {
		return "", nil, false
	}

	return fmt.Sprintf("Pattern suggestion (confidence %.1f%%):", entry.confidence*100), entry.toolSequence, true
}

// cleanupExpired removes expired patterns
func (te *trajectoryEnhancer) cleanupExpired() {
	te.mu.Lock()
	defer te.mu.Unlock()

	for hash, pattern := range te.patterns {
		if time.Since(pattern.lastUsed) > patternExpiry {
			delete(te.patterns, hash)
		}
	}
}

// getStats returns enhancer statistics
func (te *trajectoryEnhancer) getStats() map[string]interface{} {
	te.mu.RLock()
	defer te.mu.RUnlock()

	totalSuccess := 0
	totalAttempts := 0
	avgConfidence := 0.0
	for _, pattern := range te.patterns {
		totalSuccess += pattern.successCount
		totalAttempts += pattern.attemptCount
		avgConfidence += pattern.confidence
	}
	if len(te.patterns) > 0 {
		avgConfidence /= float64(len(te.patterns))
	}

	return map[string]interface{}{
		"total_patterns":    len(te.patterns),
		"total_success":     totalSuccess,
		"total_attempts":    totalAttempts,
		"avg_confidence":    avgConfidence,
		"memory_limit":      maxPatternEntries,
		"min_confidence":    minConfidence,
		"pattern_expiry_hr": patternExpiry.Hours(),
	}
}
