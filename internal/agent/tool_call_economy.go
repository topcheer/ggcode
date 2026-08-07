package agent

import (
	"fmt"
	"strings"
	"sync"
)

// toolCallEconomyState detects when the agent makes multiple sequential
// individual tool calls that could have been batched into fewer calls using
// multi-target equivalents (e.g., 3 separate read_file calls → 1 multi_file_read).
//
// This addresses the "hidden costs" highlighted in AI Agent Systems survey
// (arXiv:2601.01743) - context growth from unnecessary round-trips - and the
// overthinking loops identified in arXiv:2602.14798, where individually
// plausible tool calls compose into inflated trajectories.
//
// Key insight: each extra tool round-trip costs:
//   - 1 additional LLM inference (latency + tokens)
//   - Tool definition overhead in context
//   - Result framing overhead
//   - Increased failure surface (more steps = more error opportunities)
//
// This is a zero-LLM-cost heuristic that only triggers after detecting
// clear batchable patterns.
type toolCallEconomyState struct {
	mu sync.Mutex

	// Sliding window of recent tool names (most recent last)
	recentCalls []string
	windowSize  int

	// Per-tool consecutive counters for batchable tool detection
	consecutiveSameTool map[string]int

	// Batchable tool definitions: tool name → multi-target equivalent
	batchEquivalents map[string]string

	warnCount   int
	maxWarnings int
	fired       bool
}

func newToolCallEconomyState() *toolCallEconomyState {
	return &toolCallEconomyState{
		recentCalls:         make([]string, 0, 16),
		windowSize:          10,
		consecutiveSameTool: make(map[string]int),
		batchEquivalents: map[string]string{
			"read_file":      "multi_file_read",
			"edit_file":      "multi_edit_file or multi_file_edit",
			"write_file":     "multi_file_write",
			"list_directory": "multi_file_read or combined exploration",
			"grep":           "single regex with alternation (pattern1|pattern2)",
			"search_files":   "single regex with alternation",
			"glob":           "single glob with brace expansion (e.g., *.{go,ts})",
			"git_diff":       "single git diff for all files",
		},
		maxWarnings: 2,
	}
}

// recordCall tracks a tool call for economy analysis.
func (s *toolCallEconomyState) recordCall(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Only track tools that have batch equivalents
	if _, batchable := s.batchEquivalents[toolName]; !batchable {
		// Reset consecutive counters for all tools when a different tool is called
		s.consecutiveSameTool = make(map[string]int)
		s.recentCalls = append(s.recentCalls, toolName)
		if len(s.recentCalls) > s.windowSize {
			s.recentCalls = s.recentCalls[1:]
		}
		return
	}

	// Increment consecutive counter for this tool
	// First, check if previous call was the same tool
	if len(s.recentCalls) > 0 && s.recentCalls[len(s.recentCalls)-1] == toolName {
		s.consecutiveSameTool[toolName]++
	} else {
		// Reset all, start counting this tool
		s.consecutiveSameTool = make(map[string]int)
		s.consecutiveSameTool[toolName] = 1
	}

	s.recentCalls = append(s.recentCalls, toolName)
	if len(s.recentCalls) > s.windowSize {
		s.recentCalls = s.recentCalls[1:]
	}
}

// check returns a guidance message if tool call economy issues are detected.
func (s *toolCallEconomyState) check() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fired || s.warnCount >= s.maxWarnings {
		return ""
	}

	var msg string

	// Condition 1: 3+ consecutive calls to the same batchable tool
	for toolName, count := range s.consecutiveSameTool {
		if count >= 3 {
			equiv := s.batchEquivalents[toolName]
			msg = fmt.Sprintf(
				"[Tool Call Economy] %d consecutive '%s' calls detected. "+
					"Each individual call adds a full LLM round-trip (latency, tokens, error surface). "+
					"Consider using '%s' to batch these into fewer calls. "+
					"Research shows unnecessary tool round-trips inflate trajectories and compound failure risk "+
					"(arXiv:2601.01743, arXiv:2602.14798).",
				count, toolName, equiv,
			)
			break
		}
	}

	// Condition 2: Window has 4+ batchable tool calls (even if not all same tool)
	// indicating heavy individual-call usage that could be consolidated
	if msg == "" {
		batchableCount := 0
		for _, tc := range s.recentCalls {
			if _, b := s.batchEquivalents[tc]; b {
				batchableCount++
			}
		}
		if batchableCount >= 4 && len(s.recentCalls) >= 5 {
			msg = fmt.Sprintf(
				"[Tool Call Economy] %d of last %d calls are individual exploration/modify operations "+
					"that have multi-target equivalents. Consolidating related calls reduces context growth, "+
					"latency, and error surface. Use multi_file_read, multi_edit_file, or combined search patterns "+
					"where possible (arXiv:2601.01743).",
				batchableCount, len(s.recentCalls),
			)
		}
	}

	if msg != "" {
		s.warnCount++
		if s.warnCount >= s.maxWarnings {
			s.fired = true
		}
	}

	return msg
}

// reset clears state for a new user turn.
func (s *toolCallEconomyState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recentCalls = s.recentCalls[:0]
	s.consecutiveSameTool = make(map[string]int)
	s.warnCount = 0
	s.fired = false
}

// summary returns a compact string for logging/debugging.
func (s *toolCallEconomyState) summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	parts := make([]string, 0, len(s.consecutiveSameTool))
	for tool, count := range s.consecutiveSameTool {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s=%d", tool, count))
		}
	}
	return fmt.Sprintf("window=%d batchable_streaks=[%s] warns=%d",
		len(s.recentCalls), strings.Join(parts, ", "), s.warnCount)
}
