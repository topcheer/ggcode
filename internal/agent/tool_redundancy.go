package agent

// Tool Call Redundancy Analyzer -- Tool Use Optimization
//
// Research basis: "Tool Learning for Large Language Models" (Qin et al. 2024,
// ICLR 2024 survey) identifies tool call efficiency as a critical gap in
// LLM agents. The ACE Framework (ICLR 2026) catalogs "redundant invocation"
// as a context waste pattern. Anthropic's "Effective Context Engineering for
// Agents" (2025) recommends tracking tool call patterns to identify
// inefficiencies.
//
// Gap: ggcode has loop_detect.go which catches CONSECUTIVE identical calls
// (same tool + same args 3+ times in a row). But it resets when a different
// call is seen. This means scattered duplicates go undetected:
//
//   iter 1: grep "foo"  -> 6 results
//   iter 2: read file.go
//   iter 3: grep "foo"  -> same 6 results (loop_detect sees different prev call, resets)
//   iter 4: edit file.go
//   iter 5: grep "foo"  -> same 6 results again
//
// memoize.go caches the result (avoiding re-execution), but the agent still
// gets the cached content back and wastes attention re-analyzing it. This
// analyzer warns the agent that it has called the same tool with the same
// args N times total, suggesting it already has this information.
//
// Competitor analysis:
//   - Claude Code: no scattered duplicate detection
//   - Cursor: implicit editor context management
//   - OpenHands: event-based dedup but no frequency analysis
//   - Aider: no tracking
//
// Design:
//   - Tracks total call count per fingerprint (tool name + args hash)
//   - Warns when any fingerprint reaches the scattered threshold (3 total)
//   - Fires at most 2 times per run (advisory, not blocking)
//   - Zero LLM cost - hash map lookup

import (
	"fmt"
	"strings"
)

const (
	scatterDupWarnThreshold = 3 // total occurrences before warning
	scatterDupMaxWarnings   = 2 // max warnings per run
)

type toolRedundancyState struct {
	counts   map[string]int    // fingerprint -> total call count
	tools    map[string]string // fingerprint -> tool name (for readable messages)
	warnings int
}

func newToolRedundancyAnalyzer() *toolRedundancyState {
	return &toolRedundancyState{
		counts: make(map[string]int),
		tools:  make(map[string]string),
	}
}

func (t *toolRedundancyState) reset() {
	t.counts = make(map[string]int)
	t.tools = make(map[string]string)
	t.warnings = 0
}

// recordCall tracks a tool call and returns guidance if a scattered duplicate
// pattern is detected (same tool+args called N times, non-consecutively).
func (t *toolRedundancyState) recordCall(toolName string, args []byte) string {
	if t.warnings >= scatterDupMaxWarnings {
		// Still track for accuracy but don't warn
		fp := fingerprintToolCall(toolName, args)
		t.counts[fp]++
		return ""
	}

	fp := fingerprintToolCall(toolName, args)
	t.counts[fp]++
	t.tools[fp] = toolName

	count := t.counts[fp]
	if count == scatterDupWarnThreshold {
		t.warnings++
		return fmt.Sprintf(
			"Efficiency hint: You have called %s with these exact arguments %d times in this session "+
				"(not consecutively). The result has not changed between calls. "+
				"If you need this information, it is already in your context from earlier calls. "+
				"Avoid re-invoking the same search with identical parameters.",
			toolName, count,
		)
	}

	// Escalation at higher thresholds
	if count == scatterDupWarnThreshold*2 {
		t.warnings++
		return fmt.Sprintf(
			"Warning: %s called %d times with identical arguments. This is consuming significant iteration budget. "+
				"Consider a different approach: narrow your search, batch your operations, or use the information you already have.",
			toolName, count,
		)
	}

	return ""
}

// summary returns a human-readable summary of redundancy patterns for logging.
func (t *toolRedundancyState) summary() string {
	if len(t.counts) == 0 {
		return ""
	}
	var parts []string
	for fp, count := range t.counts {
		if count >= 2 {
			name := t.tools[fp]
			parts = append(parts, fmt.Sprintf("%s x%d", name, count))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}
