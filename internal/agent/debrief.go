package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Agent Debrief / Post-Mortem Analysis
//
// Research: Qian & Liu, "MetaAgent: Toward Self-Evolving Agent via Tool
// Meta-Learning" (arXiv:2508.00271, Aug 2025).
//
// Key insight: As agents solve tasks, they should conduct self-reflection
// and answer verification, distilling actionable experience into concise
// texts that are dynamically incorporated into future task contexts.
//
// This process (meta tool learning) enables agents to incrementally
// refine their reasoning and tool-use strategies without changing model
// parameters or requiring further post-training.
//
// THE GAP IN GGCODE:
// When a session completes (success or failure), the agent has rich
// execution data (tools used, errors encountered, strategies attempted)
// but NO mechanism to systematically extract lessons learned. Each
// session is treated as independent, wasting accumulated experience.
//
// WHAT THIS MODULE DOES:
// 1. Analyzes the completed session trace for patterns (success factors,
//    failure modes, tool effectiveness, strategy insights).
// 2. Extracts concise, actionable debrief points.
// 3. Saves them to the knowledge graph for future retrieval.
// 4. Provides debrief summary to user for transparency.
//
// Design constraints:
//   - Zero LLM cost (deterministic pattern extraction).
//   - Fires once per session at completion.
//   - Non-blocking: analysis runs after session ends.
//   - Caps debrief points at 8 per session (prevent noise).
//   - Only generates points when meaningful patterns detected.

const (
	// Max debrief points per session
	maxDebriefPoints = 8

	// Minimum tools used to consider debriefing
	minToolsForDebrief = 3

	// Minimum session duration (seconds) for debrief
	minSessionDuration = 30
)

// DebriefPoint represents a single lesson learned from a session.
// It captures actionable insights that can be retrieved in future sessions.
type DebriefPoint struct {
	Category   string    `json:"category"`   // success, failure, tool, strategy
	Title      string    `json:"title"`      // Short summary
	Detail     string    `json:"detail"`     // Actionable insight
	Confidence float64   `json:"confidence"` // 0-1, based on pattern strength
	Timestamp  time.Time `json:"timestamp"`
	SessionID  string    `json:"session_id"`
}

// DebriefAnalyzer extracts lessons from completed sessions.
// It analyzes execution traces to identify patterns and generate debrief points.
type DebriefAnalyzer struct {
	points          []*DebriefPoint
	sessionID       string
	startTime       time.Time
	endTime         time.Time
	toolCounts      map[string]int
	errorPatterns   map[string]int
	successPatterns map[string]int
}

// newDebriefAnalyzer creates a new debrief analyzer for a session.
func newDebriefAnalyzer(sessionID string) *DebriefAnalyzer {
	return &DebriefAnalyzer{
		sessionID:       sessionID,
		startTime:       time.Now(),
		toolCounts:      make(map[string]int),
		errorPatterns:   make(map[string]int),
		successPatterns: make(map[string]int),
	}
}

// recordToolCall records a tool call in the debrief trace.
func (da *DebriefAnalyzer) recordToolCall(toolName string, params map[string]interface{}, result string, isError bool) {
	da.toolCounts[toolName]++

	// Extract error patterns from failures
	if isError {
		pattern := da.extractErrorPattern(result)
		if pattern != "" {
			da.errorPatterns[pattern]++
		}
	} else {
		// Extract success patterns (e.g., specific file operations that worked)
		pattern := da.extractSuccessPattern(toolName, params)
		if pattern != "" {
			da.successPatterns[pattern]++
		}
	}
}

// extractErrorPattern extracts a canonical error pattern from error output.
func (da *DebriefAnalyzer) extractErrorPattern(errorOutput string) string {
	s := strings.ToLower(errorOutput)

	// Common error pattern categories
	patterns := []struct {
		name  string
		regex string
	}{
		{"undefined-symbol", `\b(?:undefined|undeclared|not found|not exist): (\w+)`},
		{"type-mismatch", `\b(?:cannot use|type mismatch|incompatible type)`},
		{"import-error", `\b(?:cannot find package|no such module|unknown package)`},
		{"permission-denied", `\b(?:permission denied|access denied|forbidden)`},
		{"timeout", `\b(?:timeout|timed out|deadline exceeded)`},
		{"syntax-error", `\b(?:syntax error|unexpected|expected)`},
		{"nil-dereference", `\b(?:nil pointer|nil reference|nil dereference)`},
		{"race-condition", `\b(?:data race|concurrent map)`},
		{"missing-dependency", `\b(?:module.*not found|package.*not found)`},
		{"api-error", `\b(?:api error|http status|request failed)`},
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p.regex)
		if re.MatchString(s) {
			return p.name
		}
	}

	return ""
}

// extractSuccessPattern extracts a success pattern from successful tool calls.
func (da *DebriefAnalyzer) extractSuccessPattern(toolName string, params map[string]interface{}) string {
	switch toolName {
	case "edit_file", "multi_edit_file", "write_file":
		if path, ok := params["path"].(string); ok {
			// Extract file type pattern
			ext := ""
			if idx := strings.LastIndex(path, "."); idx > 0 {
				ext = path[idx:]
			}
			if ext != "" {
				return fmt.Sprintf("edit-%s", ext)
			}
		}
	case "run_command", "start_command":
		if cmd, ok := params["command"].(string); ok {
			// Extract command type (go test, make, npm, etc.)
			parts := strings.Fields(cmd)
			if len(parts) > 0 {
				base := strings.TrimPrefix(parts[0], "#")
				return fmt.Sprintf("cmd-%s", base)
			}
		}
	}
	return ""
}

// finalize generates debrief points from the accumulated trace.
func (da *DebriefAnalyzer) finalize() []*DebriefPoint {
	da.endTime = time.Now()
	duration := da.endTime.Sub(da.startTime).Seconds()

	// Skip debrief for very short or minimal sessions
	if len(da.toolCounts) < minToolsForDebrief || duration < minSessionDuration {
		return nil
	}

	da.extractFailurePatterns()
	da.extractSuccessPatterns()
	da.extractToolInsights()
	da.extractStrategyInsights()

	// Sort by confidence and cap
	slices.SortFunc(da.points, func(a, b *DebriefPoint) int {
		if a.Confidence != b.Confidence {
			return int((b.Confidence - a.Confidence) * 1000)
		}
		return 0
	})

	if len(da.points) > maxDebriefPoints {
		da.points = da.points[:maxDebriefPoints]
	}

	return da.points
}

// extractFailurePatterns generates debrief points from error patterns.
func (da *DebriefAnalyzer) extractFailurePatterns() {
	for pattern, count := range da.errorPatterns {
		if count < 2 {
			continue // Ignore one-off errors
		}

		confidence := minFloat(float64(count)/float64(len(da.toolCounts)), 1.0)
		point := &DebriefPoint{
			Category:   "failure",
			Title:      fmt.Sprintf("Recurring %s", da.humanizeErrorPattern(pattern)),
			Detail:     fmt.Sprintf("Encountered %d %s errors. Check code for common causes (type mismatches, missing imports, etc.).", count, da.humanizeErrorPattern(pattern)),
			Confidence: confidence,
			Timestamp:  time.Now(),
			SessionID:  da.sessionID,
		}
		da.points = append(da.points, point)
	}
}

// extractSuccessPatterns generates debrief points from successful patterns.
func (da *DebriefAnalyzer) extractSuccessPatterns() {
	for pattern, count := range da.successPatterns {
		if count < 3 {
			continue // Need consistent pattern
		}

		confidence := minFloat(float64(count)/float64(len(da.toolCounts))*1.5, 1.0)
		point := &DebriefPoint{
			Category:   "success",
			Title:      fmt.Sprintf("Effective %s strategy", pattern),
			Detail:     fmt.Sprintf("Successfully used %s %d times. This approach appears reliable for this task type.", pattern, count),
			Confidence: confidence,
			Timestamp:  time.Now(),
			SessionID:  da.sessionID,
		}
		da.points = append(da.points, point)
	}
}

// extractToolInsights generates debrief points about tool usage.
func (da *DebriefAnalyzer) extractToolInsights() {
	// Identify most-used tools
	totalTools := 0
	for _, count := range da.toolCounts {
		totalTools += count
	}

	for tool, count := range da.toolCounts {
		usageRatio := float64(count) / float64(totalTools)

		// Flag over-reliance on a single tool (potential tool tunnel vision)
		if usageRatio > 0.5 && totalTools > 5 {
			point := &DebriefPoint{
				Category:   "strategy",
				Title:      fmt.Sprintf("High reliance on %s", tool),
				Detail:     fmt.Sprintf("Used %s for %.0f%% of all tool calls. Consider diversifying tool usage to avoid tunnel vision.", tool, usageRatio*100),
				Confidence: usageRatio,
				Timestamp:  time.Now(),
				SessionID:  da.sessionID,
			}
			da.points = append(da.points, point)
		}
	}
}

// extractStrategyInsights generates high-level strategy debrief points.
func (da *DebriefAnalyzer) extractStrategyInsights() {
	// Check for edit oscillation pattern (same file edited multiple times)
	fileEdits := make(map[string]int)
	for tool, count := range da.toolCounts {
		if strings.Contains(tool, "edit") || strings.Contains(tool, "write") {
			// This is a rough heuristic; in practice we'd track file paths
			fileEdits[tool] = count
		}
	}

	// If many edit tools were used with high counts, flag potential inefficiency
	editCount := 0
	for _, count := range fileEdits {
		editCount += count
	}

	if editCount > 5 && editCount > len(da.toolCounts)/2 {
		point := &DebriefPoint{
			Category:   "strategy",
			Title:      "High edit iteration count",
			Detail:     fmt.Sprintf("Made %d edit operations. Consider analyzing whether more upfront planning could reduce back-and-forth edits.", editCount),
			Confidence: 0.7,
			Timestamp:  time.Now(),
			SessionID:  da.sessionID,
		}
		da.points = append(da.points, point)
	}
}

// humanizeErrorPattern converts a pattern name to human-readable form.
// Uses string transformations rather than hardcoded lookup table.
func (da *DebriefAnalyzer) humanizeErrorPattern(pattern string) string {
	// Replace hyphens with spaces and capitalize first letter
	result := strings.ReplaceAll(pattern, "-", " ")
	if len(result) == 0 {
		return pattern
	}
	return strings.ToUpper(result[:1]) + result[1:]
}

// toJSON serializes debrief points to JSON for storage.
func (dp *DebriefPoint) toJSON() (string, error) {
	data, err := json.MarshalIndent(dp, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// formatSummary returns a human-readable summary of debrief points.
func (da *DebriefAnalyzer) formatSummary() string {
	if len(da.points) == 0 {
		return "No significant patterns detected in this session."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n=== Session Debrief (%d insights) ===\n", len(da.points)))

	for i, point := range da.points {
		sb.WriteString(fmt.Sprintf("\n%d. [%s] %s\n", i+1, strings.ToUpper(point.Category), point.Title))
		sb.WriteString(fmt.Sprintf("   %s\n", point.Detail))
		if point.Confidence < 0.7 {
			sb.WriteString(fmt.Sprintf("   (Confidence: %.0f%%)\n", point.Confidence*100))
		}
	}

	sb.WriteString("\n=====================================\n")
	return sb.String()
}

// minFloat returns the minimum of two floats.
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
