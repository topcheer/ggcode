package agent

import (
	"fmt"
	"sync"
)

// skillDiscovery tracks tool call patterns to automatically detect
// recurring workflows that could be abstracted as reusable skills.
// Implements the Skill Learning principle from ELL framework (arXiv:2508.19005).
type skillDiscovery struct {
	mu             sync.RWMutex
	recentCalls    []toolCallSnapshot // sliding window of recent tool invocations
	patternCounts  map[string]int     // frequency of detected patterns
	minPatternLen  int                // minimum sequence length to consider
	windowSize     int                // max recent calls to track
	maxSuggestions int                // max suggestions per run
	suggested      int                // suggestions made in current run
}

type toolCallSnapshot struct {
	toolName string
	// Simplified signature: just the tool name for pattern detection
	// Future: could include parameter templates for more precise patterns
}

const (
	// sdMinPatternLen is the minimum tool call sequence length to be considered a pattern
	sdMinPatternLen = 3
	// sdWindowSize is the sliding window size for recent tool calls
	sdWindowSize = 20
	// sdMaxSuggestions is the maximum skill suggestions to emit per run
	sdMaxSuggestions = 2
)

func newSkillDiscovery() *skillDiscovery {
	return &skillDiscovery{
		recentCalls:    make([]toolCallSnapshot, 0, sdWindowSize),
		patternCounts:  make(map[string]int),
		minPatternLen:  sdMinPatternLen,
		windowSize:     sdWindowSize,
		maxSuggestions: sdMaxSuggestions,
	}
}

// recordCall adds a tool call to the recent history window.
func (sd *skillDiscovery) recordCall(toolName string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.recentCalls = append(sd.recentCalls, toolCallSnapshot{toolName: toolName})
	if len(sd.recentCalls) > sd.windowSize {
		// Sliding window: keep only the most recent calls
		sd.recentCalls = sd.recentCalls[len(sd.recentCalls)-sd.windowSize:]
	}

	// Detect patterns after each call
	sd.detectPatterns()
}

// detectPatterns scans the recent call history for recurring sequences.
// It uses a simple n-gram frequency analysis approach.
func (sd *skillDiscovery) detectPatterns() {
	if len(sd.recentCalls) < sd.minPatternLen {
		return
	}

	// Extract all possible sequences of length minPatternLen
	for i := 0; i <= len(sd.recentCalls)-sd.minPatternLen; i++ {
		sequence := sd.recentCalls[i : i+sd.minPatternLen]
		key := sequenceKey(sequence)
		if key != "" {
			sd.patternCounts[key]++
		}
	}
}

// sequenceKey converts a tool call sequence to a string key for pattern matching.
func sequenceKey(calls []toolCallSnapshot) string {
	if len(calls) == 0 {
		return ""
	}
	key := calls[0].toolName
	for i := 1; i < len(calls); i++ {
		key += "→" + calls[i].toolName
	}
	return key
}

// checkForSuggestions analyzes pattern frequency and suggests skills
// when a pattern recurs. Returns guidance text if a new pattern is detected.
func (sd *skillDiscovery) checkForSuggestions() string {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.suggested >= sd.maxSuggestions {
		return ""
	}

	// Find patterns that have occurred at least twice (potential candidates)
	for key, count := range sd.patternCounts {
		if count >= 2 {
			sd.suggested++
			return sd.formatSuggestion(key, count)
		}
	}

	return ""
}

// formatSuggestion creates user-facing guidance for a detected pattern.
func (sd *skillDiscovery) formatSuggestion(key string, count int) string {
	return fmt.Sprintf(
		"[Skill Discovery] Detected recurring tool pattern (%dx): %s\n"+
			"This workflow could be abstracted as a reusable skill. Consider:\n"+
			"  1. Run /skill:save to capture this pattern as a skill\n"+
			"  2. Future similar tasks can reuse the skill for efficiency\n"+
			"  3. Skills enable consistent application of proven workflows",
		count, key,
	)
}

// reset clears state for a new run.
func (sd *skillDiscovery) reset() {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.recentCalls = sd.recentCalls[:0]
	sd.patternCounts = make(map[string]int)
	sd.suggested = 0
}
