package agent

// Trajectory Pattern Learner
//
// Research basis:
//   - Agent-R (arXiv:2506.15832, 2025)
//     Self-training from successful trajectories: agents learn by identifying
//     and reusing patterns from their own successful action sequences.
//   - Reflexion (Shinn et al., 2023)
//     Self-improvement through reflection on past trajectories.
//   - "Agentic Metacognition" (Xu, arXiv:2509.19783)
//     Meta-learning about what works in one's own behavior.
//
// Problem: ggcode has extensive failure detection (27+ detectors) but no
// mechanism to learn from success. The agent repeats successful patterns
// by chance rather than by design.
//
// Gap: No trajectory pattern learning - the agent cannot identify,
// encode, or reuse patterns from its own successful action sequences.
//
// Design:
//   - Tracks successful tool sequences (error-free, led to completion)
//   - Identifies recurring 2-3 tool patterns
//   - Provides pattern-based guidance when similar context detected
//   - Zero LLM cost - deterministic pattern matching
//   - Complements trajectory_health.go (which detects unhealthy patterns)

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// patternMinOccurrences: minimum times a pattern must recur to be learned
	patternMinOccurrences = 3

	// patternMaxLength: max pattern length to track (2-3 tool sequences)
	patternMaxLength = 3

	// patternSuggestionLimit: max pattern suggestions to emit per run
	patternSuggestionLimit = 2
)

// toolPattern represents a recurring successful tool sequence.
type toolPattern struct {
	sequence    []string // tool names in order
	occurrences int      // how often this pattern succeeded
	contexts    []string // file types or domains where pattern worked
}

// patternLearner tracks and learns from successful trajectories.
type patternLearner struct {
	mu             sync.Mutex
	successfulSeqs [][]string     // recent successful tool sequences
	patterns       map[string]int // pattern key -> occurrence count
	suggestions    int            // number of suggestions made this run
	sequenceBuffer []string       // current in-progress sequence
	hadError       bool           // did current sequence have an error?
}

func newPatternLearner() *patternLearner {
	return &patternLearner{
		patterns: make(map[string]int),
	}
}

func (p *patternLearner) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.successfulSeqs = nil
	p.patterns = make(map[string]int)
	p.suggestions = 0
	p.sequenceBuffer = nil
	p.hadError = false
}

// recordToolCall records a tool call in the current sequence.
func (p *patternLearner) recordToolCall(name string, hadError bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if hadError {
		// Mark sequence as failed, reset buffer
		p.hadError = true
		p.sequenceBuffer = nil
		return
	}

	p.sequenceBuffer = append(p.sequenceBuffer, name)
}

// completeIteration is called at the end of each iteration.
// If the iteration was error-free, record the sequence as successful.
func (p *patternLearner) completeIteration(wasSuccessful bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !wasSuccessful || p.hadError || len(p.sequenceBuffer) == 0 {
		p.sequenceBuffer = nil
		p.hadError = false
		return
	}

	// Record successful sequence
	if len(p.sequenceBuffer) > 1 {
		seqCopy := make([]string, len(p.sequenceBuffer))
		copy(seqCopy, p.sequenceBuffer)
		p.successfulSeqs = append(p.successfulSeqs, seqCopy)

		// Extract patterns from this sequence
		p.extractPatterns(seqCopy)
	}

	// Keep only recent sequences (last 20)
	if len(p.successfulSeqs) > 20 {
		p.successfulSeqs = p.successfulSeqs[len(p.successfulSeqs)-20:]
	}

	p.sequenceBuffer = nil
	p.hadError = false
}

// extractPatterns identifies recurring subsequences in a successful sequence.
func (p *patternLearner) extractPatterns(sequence []string) {
	n := len(sequence)

	// Extract patterns of length 2-3
	for length := 2; length <= patternMaxLength && length <= n; length++ {
		for i := 0; i <= n-length; i++ {
			pattern := sequence[i : i+length]
			key := strings.Join(pattern, "→")

			p.patterns[key]++
		}
	}

	// Note: We don't prune after each extraction to allow accumulation
	// across iterations. Pruning happens in assess() or can be done explicitly.
}

// suggestPatterns returns guidance based on learned successful patterns.
// Returns empty string if no relevant pattern found.
func (p *patternLearner) suggestPatterns(currentSequence []string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.suggestions >= patternSuggestionLimit {
		return ""
	}

	if len(p.patterns) == 0 {
		return ""
	}

	// Find patterns that start with the last 1-2 tools in current sequence
	var matches []string
	currentKey := strings.Join(currentSequence, "→")

	for patternKey, count := range p.patterns {
		// Check if pattern extends current sequence
		if strings.HasPrefix(patternKey, currentKey) && patternKey != currentKey {
			tools := strings.Split(patternKey, "→")
			// Get the continuation (tools after current sequence)
			currentLen := len(currentSequence)
			if len(tools) > currentLen {
				continuation := tools[currentLen:]
				matches = append(matches, fmt.Sprintf("%s (%dx learned)", strings.Join(continuation, " → "), count))
			}
		}
	}

	if len(matches) == 0 {
		return ""
	}

	p.suggestions++

	// Pick the most frequent pattern
	var bestMatch string
	var bestCount int
	for _, m := range matches {
		// Parse count from suggestion
		if idx := strings.Index(m, "("); idx != -1 {
			countStr := strings.TrimSuffix(m[idx+1:len(m)-1], "x learned")
			var count int
			fmt.Sscanf(countStr, "%d", &count)
			if count > bestCount {
				bestCount = count
				bestMatch = m
			}
		}
	}

	if bestMatch == "" && len(matches) > 0 {
		bestMatch = matches[0]
	}

	if bestMatch == "" {
		return ""
	}

	return fmt.Sprintf(
		"[pattern-learning] Based on your recent successful trajectories, this "+
			"tool sequence has worked well in similar contexts: %s. Consider following "+
			"this established pattern for consistency.",
		bestMatch,
	)
}

// checkPatternSuggestion is called from the agent loop to provide pattern guidance.
// It looks at the recent tool sequence and suggests learned successful patterns.
func (a *Agent) checkPatternSuggestion(recentTools []string) string {
	if a.patternLearner == nil {
		return ""
	}

	// Use last 2 tools as context for pattern matching
	contextLen := 2
	if len(recentTools) < contextLen {
		contextLen = len(recentTools)
	}
	if contextLen == 0 {
		return ""
	}

	return a.patternLearner.suggestPatterns(recentTools[len(recentTools)-contextLen:])
}

// getRecentToolNames extracts tool names from recent tool call deltas.
func getRecentToolNames(toolCalls []provider.ToolCallDelta) []string {
	var names []string
	for _, tc := range toolCalls {
		if tc.Name != "" {
			names = append(names, tc.Name)
		}
	}
	return names
}
