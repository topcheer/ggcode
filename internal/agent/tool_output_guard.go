package agent

import (
	"fmt"
	"strings"
)

// Context-fill-aware tool output truncation.
//
// Research basis: Context engineering (Anthropic 2025, Fundesk 2026) identifies
// "budget by fill %" as a top technique. Chroma's 2025 study found all frontier
// models degrade past ~50% context fullness. Tool output is the "silent context
// killer" — a single 50KB build log consumes ~12K tokens immediately.
//
// This guard applies progressive truncation to large tool results BEFORE they
// enter context, scaling aggressiveness with context fill level:
//   - < 50% fill: no truncation (let tools handle their own limits)
//   - 50-65% fill: truncate results > 40KB to 40KB
//   - 65-75% fill: truncate results > 20KB to 20KB
//   - 75%+ fill: truncate results > 10KB to 10KB
//
// Uses head-tail preservation (first 40% + last 50% + truncation marker) so
// the agent sees both the beginning (context) and end (errors/results).
// No LLM cost — purely mechanical.

const (
	// Context fill thresholds (fraction of compaction threshold).
	contextFillModerate = 0.50 // Start being conservative
	contextFillHigh     = 0.65 // More aggressive
	contextFillCritical = 0.75 // Maximum aggressiveness

	// Output size limits at each fill level.
	outputLimitModerate = 40 * 1024 // 40KB at 50% fill
	outputLimitHigh     = 20 * 1024 // 20KB at 65% fill
	outputLimitCritical = 10 * 1024 // 10KB at 75% fill
)

// guardToolOutput truncates large tool results based on context fill level.
// contextFill is the ratio of current tokens to compaction threshold (0.0-1.0+).
// Returns the (possibly truncated) content.
func guardToolOutput(content string, contextFill float64) string {
	if contextFill < contextFillModerate {
		return content
	}

	limit := outputLimitModerate
	switch {
	case contextFill >= contextFillCritical:
		limit = outputLimitCritical
	case contextFill >= contextFillHigh:
		limit = outputLimitHigh
	}

	if len(content) <= limit {
		return content
	}

	return truncateHeadTail(content, limit)
}

// truncateHeadTail keeps the first ~40% and last ~50% of content, with a
// truncation marker in between. Snaps to line boundaries for readability.
func truncateHeadTail(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// Reserve space for the truncation marker.
	marker := fmt.Sprintf("\n\n[... output truncated: %s total, showing head + tail ...]\n\n", formatBytes(len(s)))
	usable := maxLen - len(marker)
	if usable < 1000 {
		// Limit too small for meaningful truncation; just hard-cut.
		return s[:maxLen]
	}

	headLen := usable * 2 / 5 // 40% head
	tailLen := usable * 3 / 5 // 50% tail (errors/results at end are more important)

	head := s[:headLen]
	tail := s[len(s)-tailLen:]

	// Snap to line boundaries for cleaner output.
	if idx := strings.LastIndex(head, "\n"); idx > headLen/2 {
		head = head[:idx]
	}
	if idx := strings.Index(tail, "\n"); idx >= 0 && idx < tailLen/2 {
		tail = tail[idx+1:]
	}

	return head + marker + tail
}

func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
