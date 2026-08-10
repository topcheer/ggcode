package agent

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Active Context Compression (ACC)
// Research: arXiv "Active Context Compression: Autonomous Memory Management in LLM Agents"
// Inspired by Physarum polycephalum - the agent autonomously decides when to consolidate
// learning and prune raw interaction history to manage context growth.
//
// Key principles:
// 1. Autonomous decision: agent decides when to compress, not external trigger
// 2. Preservation: raw trajectory preserved, compressed summary generated
// 3. Adaptive threshold: compression trigger adjusts based on context pressure
//
// This is different from static compression (fixed windows) or reactive compact (on overflow).

// accState tracks the active compression state
type accState struct {
	lastActionTime    time.Time
	actionCount       int
	compressionCount  int
	triggerThreshold  float64 // Adaptive threshold (0.0-1.0)
	lastTriggerReason string
}

// accConfig holds configuration for active compression
type accConfig struct {
	minActionCount    int           // Minimum actions before considering compression
	minTimeBetween    time.Duration // Minimum time between compressions
	baseThreshold     float64       // Base compression trigger threshold (0.0-1.0)
	thresholdDecay    float64       // How fast threshold decays with context pressure
	maxCompressionRun int           // Max compressions per session
}

// defaultACCConfig returns default active compression configuration
func defaultACCConfig() *accConfig {
	return &accConfig{
		minActionCount:    5,
		minTimeBetween:    30 * time.Second,
		baseThreshold:     0.6,
		thresholdDecay:    0.1,
		maxCompressionRun: 3,
	}
}

// calculateCompressionScore computes a compression trigger score (0.0-1.0)
// Higher score = stronger need to compress
func (a *Agent) calculateCompressionScore(messages []provider.ContentBlock) float64 {
	if len(messages) == 0 {
		return 0.0
	}

	score := 0.0

	// Factor 1: Message count pressure (0.0-0.3)
	msgCount := float64(len(messages))
	if msgCount > 20 {
		score += math.Min(0.3, (msgCount-20)/100.0)
	}

	// Factor 2: Token estimation (0.0-0.4)
	totalTokens := 0.0
	for _, msg := range messages {
		totalTokens += float64(len(msg.Text)) / 4.0 // Rough token estimate
	}
	if totalTokens > 8000 {
		score += math.Min(0.4, (totalTokens-8000)/16000.0)
	}

	// Factor 3: Repetition detection (0.0-0.3)
	repetitionScore := a.detectRepetition(messages)
	score += repetitionScore * 0.3

	return math.Min(1.0, score)
}

// detectRepetition detects repetitive patterns in message history
// Returns 0.0 (no repetition) to 1.0 (high repetition)
func (a *Agent) detectRepetition(messages []provider.ContentBlock) float64 {
	if len(messages) < 4 {
		return 0.0
	}

	// Sample recent text blocks for similarity
	samples := make([]string, 0, 4)
	for i := len(messages) - 1; i >= 0 && len(samples) < 4; i-- {
		if messages[i].Type == "text" && len(messages[i].Text) > 50 {
			samples = append(samples, strings.ToLower(messages[i].Text[:50]))
		}
	}

	if len(samples) < 2 {
		return 0.0
	}

	// Calculate pairwise similarity
	similarPairs := 0
	for i := 0; i < len(samples)-1; i++ {
		if similarity(samples[i], samples[i+1]) > 0.7 {
			similarPairs++
		}
	}

	return float64(similarPairs) / float64(len(samples)-1)
}

// similarity calculates simple Jaccard-like similarity between two strings
func similarity(s1, s2 string) float64 {
	if s1 == "" || s2 == "" {
		return 0.0
	}

	// Identical strings
	if s1 == s2 {
		return 1.0
	}

	words1 := make(map[string]bool)
	for _, w := range strings.Fields(s1) {
		words1[w] = true
	}

	words2 := make(map[string]bool)
	for _, w := range strings.Fields(s2) {
		words2[w] = true
	}

	intersection := 0
	union := len(words1) + len(words2)

	for w := range words1 {
		if words2[w] {
			intersection++
		}
	}

	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// shouldTriggerCompression determines if compression should be triggered
func (a *Agent) shouldTriggerCompression(state *accState, config *accConfig, messages []provider.ContentBlock) (bool, string) {
	// Minimum action count check
	if state.actionCount < config.minActionCount {
		return false, "insufficient actions"
	}

	// Minimum time between compressions
	if time.Since(state.lastActionTime) < config.minTimeBetween {
		return false, "too soon since last compression"
	}

	// Maximum compression count check
	if state.compressionCount >= config.maxCompressionRun {
		return false, "max compressions reached"
	}

	// Calculate compression score
	score := a.calculateCompressionScore(messages)

	// Adaptive threshold: decays with context pressure
	// As context grows, threshold lowers, making compression easier to trigger
	adaptiveThreshold := config.baseThreshold - (float64(state.compressionCount) * config.thresholdDecay)
	adaptiveThreshold = math.Max(0.3, adaptiveThreshold) // Floor at 0.3

	shouldTrigger := score >= adaptiveThreshold
	reason := fmt.Sprintf("score=%.2f vs threshold=%.2f", score, adaptiveThreshold)

	return shouldTrigger, reason
}

// compressHistory generates a compressed summary of message history
// This is a lightweight placeholder - real implementation would use LLM to generate summary
func (a *Agent) compressHistory(messages []provider.ContentBlock) string {
	if len(messages) == 0 {
		return ""
	}

	// Count message types
	textMsgs := 0
	toolUses := 0

	for _, msg := range messages {
		switch msg.Type {
		case "text":
			textMsgs++
		case "tool_use":
			toolUses++
		}
	}

	// Extract key topics (simple heuristic)
	topics := a.extractTopics(messages)

	// Generate summary
	summary := fmt.Sprintf(
		"[COMPRESSED: %d text messages, %d tool uses]",
		textMsgs, toolUses,
	)

	if len(topics) > 0 {
		summary += fmt.Sprintf(" Topics: %s", strings.Join(topics, ", "))
	}

	return summary
}

// extractTopics extracts key topics from message history
func (a *Agent) extractTopics(messages []provider.ContentBlock) []string {
	// Simple keyword-based topic extraction
	keywords := map[string]int{
		"bug":         0,
		"fix":         0,
		"test":        0,
		"feature":     0,
		"refactor":    0,
		"deploy":      0,
		"config":      0,
		"build":       0,
		"api":         0,
		"database":    0,
		"error":       0,
		"performance": 0,
	}

	for _, msg := range messages {
		text := strings.ToLower(msg.Text)
		for kw := range keywords {
			if strings.Contains(text, kw) {
				keywords[kw]++
			}
		}
	}

	// Return top 3 keywords with count > 0
	topics := make([]string, 0, 3)
	for kw, count := range keywords {
		if count > 0 {
			topics = append(topics, kw)
		}
	}

	if len(topics) > 3 {
		topics = topics[:3]
	}

	return topics
}

// checkActiveCompression is called after each action to evaluate compression need
func (a *Agent) checkActiveCompression(_ *accState, _ *accConfig) {
	// Placeholder for autonomous compression trigger logic
	// This would evaluate context pressure and decide whether to compress
	debug.Log("ACC", "Checking compression need")
}

// getACCState returns the current ACC state from agent metadata
func (a *Agent) getACCState() *accState {
	if a.metadata == nil {
		a.metadata = make(map[string]string)
	}

	state := &accState{
		lastActionTime:   time.Now(),
		actionCount:      0,
		compressionCount: 0,
		triggerThreshold: 0.6,
	}

	// Restore from metadata if available
	if cc, ok := a.metadata["acc_compression_count"]; ok {
		fmt.Sscanf(cc, "%d", &state.compressionCount)
	}
	if lt, ok := a.metadata["acc_last_time"]; ok {
		if t, err := time.Parse(time.RFC3339, lt); err == nil {
			state.lastActionTime = t
		}
	}

	return state
}

// saveACCState saves the ACC state to agent metadata
func (a *Agent) saveACCState(state *accState) {
	if a.metadata == nil {
		a.metadata = make(map[string]string)
	}

	a.metadata["acc_compression_count"] = fmt.Sprintf("%d", state.compressionCount)
	a.metadata["acc_last_time"] = state.lastActionTime.Format(time.RFC3339)
}
