package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
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
		debug.Log("context-guard", "no-truncation fill=%.2f len=%d", contextFill, len(content))
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
		debug.Log("context-guard", "below-limit fill=%.2f limit=%d len=%d", contextFill, limit, len(content))
		return content
	}

	truncated := truncateHeadTail(content, limit)
	debug.Log("context-guard", "truncated fill=%.2f limit=%d before=%d after=%d", contextFill, limit, len(content), len(truncated))
	return truncated
}

// truncateHeadTail keeps the first ~40% and last ~50% of content, with a
// truncation marker in between. It also extracts critical lines (errors,
// panics, test failures, etc.) from the truncated middle section so the
// agent doesn't lose actionable diagnostics buried in large outputs.
// Snaps to rune boundaries to prevent UTF-8 corruption, then to line
// boundaries for readability.
func truncateHeadTail(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	// Reserve space for the truncation marker.
	marker := fmt.Sprintf("\n\n[... output truncated: %s total, showing head + tail ...]\n\n", formatBytes(len(s)))
	usable := maxLen - len(marker)
	if usable < 1000 {
		// Limit too small for meaningful truncation; hard-cut at rune boundary.
		return s[:util.SnapToRuneStart(s, maxLen)]
	}

	// First pass: rough head-tail split to identify the middle section.
	roughHeadLen := usable * 2 / 5
	roughTailLen := usable * 3 / 5
	roughTailStart := len(s) - roughTailLen
	if roughTailStart < roughHeadLen {
		roughTailStart = roughHeadLen
	}

	// Extract critical lines from the middle section (the part that will be
	// discarded). If highlights are found, reserve budget for them and shrink
	// the head-tail allocation proportionally. When no critical lines exist,
	// behavior is identical to the original implementation.
	var highlightSection string
	contentBudget := usable
	if roughTailStart > roughHeadLen {
		middle := s[roughHeadLen:roughTailStart]
		highlights := extractCriticalLines(middle)
		if len(highlights) > 0 {
			maxHL := usable / 7 // ~14% of usable budget for highlights
			if hlText := formatHighlights(highlights, maxHL); hlText != "" {
				highlightSection = hlText
				contentBudget = usable - len(highlightSection)
			}
		}
	}

	headLen := contentBudget * 2 / 5 // 40% head
	tailLen := contentBudget * 3 / 5 // 50% tail (errors/results at end are more important)

	// Snap byte offsets to rune boundaries to prevent UTF-8 corruption.
	head := s[:util.SnapToRuneStart(s, headLen)]
	tailStart := util.SnapToRuneStart(s, len(s)-tailLen)
	tail := s[tailStart:]

	// Snap to line boundaries for cleaner output.
	if idx := strings.LastIndex(head, "\n"); idx > headLen/2 {
		head = head[:idx]
	}
	if idx := strings.Index(tail, "\n"); idx >= 0 && idx < tailLen/2 {
		tail = tail[idx+1:]
	}

	return head + marker + highlightSection + tail
}

// criticalLinePatterns are substrings that indicate an actionable diagnostic
// line worth preserving during truncation. Matching is case-insensitive.
var criticalLinePatterns = []string{
	"panic:", "panic(", "fatal error", "fatal:",
	"error:", "error ", " errors ",
	"undefined:", "undefined reference", "undefined symbol",
	"cannot find", "not found", "no such file",
	"exception", "traceback",
	"fail:", "fail ", "failed", "failure",
	"segmentation fault", "sigsegv",
	"assertion failed", "assert failed",
	"warning:", "deprecated",
	"securitywarning", "vulnerability",
	"syntaxerror", "indentationerror",
	"permission denied", "access denied",
	"timeout", "timed out",
	"exit code", "exit status",
}

// maxCriticalLines caps the number of extracted highlight lines to avoid
// overwhelming the context budget with diagnostics from very noisy logs.
const maxCriticalLines = 20

// maxCriticalLineLen skips lines longer than this — they are typically stack
// traces or data dumps that are too verbose to be useful as highlights.
const maxCriticalLineLen = 300

// extractCriticalLines scans content for lines containing error, warning, or
// failure patterns. Returns deduplicated lines in order of first appearance.
func extractCriticalLines(s string) []string {
	lines := strings.Split(s, "\n")
	var critical []string
	seen := make(map[string]bool)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 0 || len(trimmed) > maxCriticalLineLen {
			continue
		}
		lower := strings.ToLower(trimmed)
		matched := false
		for _, pattern := range criticalLinePatterns {
			if strings.Contains(lower, pattern) {
				matched = true
				break
			}
		}
		if matched && !seen[trimmed] {
			seen[trimmed] = true
			critical = append(critical, trimmed)
			if len(critical) >= maxCriticalLines {
				break
			}
		}
	}
	return critical
}

// formatHighlights renders extracted critical lines into a compact section
// suitable for insertion into truncated output. Returns empty string if the
// formatted result would exceed maxLen or no lines are provided.
func formatHighlights(lines []string, maxLen int) string {
	if len(lines) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[... %d key lines from truncated section ...]\n", len(lines)))
	for i, line := range lines {
		entry := line + "\n"
		remaining := len(lines) - i - 1
		footer := "\n\n"
		if remaining > 0 {
			footer = fmt.Sprintf("  (... %d more ...)\n\n", remaining)
		}
		if sb.Len()+len(entry)+len(footer) > maxLen {
			if remaining > 0 {
				sb.WriteString(fmt.Sprintf("  (... %d more ...)\n", remaining))
			}
			break
		}
		sb.WriteString(entry)
	}
	sb.WriteString("\n")
	return sb.String()
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
