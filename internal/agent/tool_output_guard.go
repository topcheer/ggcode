package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	contextFillModerate = 0.50
	contextFillHigh     = 0.65
	contextFillCritical = 0.75

	outputLimitModerate = 40 * 1024
	outputLimitHigh     = 20 * 1024
	outputLimitCritical = 10 * 1024
)

const toolHeadTailTruncationPrefix = "[... output truncated:"

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

func truncateHeadTail(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	marker := fmt.Sprintf("\n\n%s %s total, showing head + tail ...]\n\n", toolHeadTailTruncationPrefix, formatBytes(len(s)))
	usable := maxLen - len(marker)
	if usable < 1000 {

		return s[:util.SnapToRuneStart(s, maxLen)]
	}

	roughHeadLen := usable * 2 / 5
	roughTailLen := usable * 3 / 5
	roughTailStart := len(s) - roughTailLen
	if roughTailStart < roughHeadLen {
		roughTailStart = roughHeadLen
	}

	var highlightSection string
	contentBudget := usable
	if roughTailStart > roughHeadLen {
		middle := s[roughHeadLen:roughTailStart]
		highlights := extractCriticalLines(middle)
		if len(highlights) > 0 {
			maxHL := usable / 7
			if hlText := formatHighlights(highlights, maxHL); hlText != "" {
				highlightSection = hlText
				contentBudget = usable - len(highlightSection)
			}
		}
	}

	headLen := contentBudget * 2 / 5
	tailLen := contentBudget * 3 / 5

	head := s[:util.SnapToRuneStart(s, headLen)]
	tailStart := util.SnapToRuneStart(s, len(s)-tailLen)
	tail := s[tailStart:]

	if idx := strings.LastIndex(head, "\n"); idx > headLen/2 {
		head = head[:idx]
	}
	if idx := strings.Index(tail, "\n"); idx >= 0 && idx < tailLen/2 {
		tail = tail[idx+1:]
	}

	return head + marker + highlightSection + tail
}

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

const maxCriticalLines = 20

const maxCriticalLineLen = 300

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
