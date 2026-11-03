package tool

import (
	"fmt"
	"strings"
)

var acceptanceHeadings = []string{
	"acceptance criteria",
	"acceptance",
	"success criteria",
	"definition of done",
	"done when",
	"done:",
}

const maxAcceptanceItems = 10

const maxAcceptanceItemLen = 200

const acceptanceMarker = "=== ACCEPTANCE CHECKPOINT (delegation validation) ==="

func extractAcceptanceCriteria(task string) []string {
	if task == "" {
		return nil
	}
	lines := strings.Split(task, "\n")
	var items []string
	inSection := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		stripped := strings.TrimLeft(line, "#- ")
		stripped = strings.TrimSpace(stripped)
		lower := strings.ToLower(stripped)

		if !inSection {
			for _, h := range acceptanceHeadings {
				if strings.HasPrefix(lower, h) {
					inSection = true

					if rest := textAfterMarker(lower, h); rest != "" {
						items = appendCriterion(items, line)
					}
					break
				}
			}
			continue
		}

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "=== ") {
			break
		}
		next := nextHeadingKeyword(lower)
		if next != "" && !isListLine(line) {
			break
		}
		if isListLine(line) || len(line) > 0 {
			items = appendCriterion(items, line)
		}
	}
	return items
}

func stripListPrefix(s string) string {
	b := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "-*•"))
	i := 0
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(b) {
		return b
	}
	if b[i] == '.' || b[i] == ')' {
		rest := b[i+1:]
		if b[i] == '.' && len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			return b
		}
		return strings.TrimSpace(rest)
	}
	return b
}

func appendCriterion(items []string, line string) []string {
	if len(items) >= maxAcceptanceItems {
		return items
	}
	c := stripListPrefix(line)
	if c == "" {
		return items
	}
	if len(c) > maxAcceptanceItemLen {
		c = truncateUTF8Safe(c, maxAcceptanceItemLen) + "..."
	}
	return append(items, c)
}

func textAfterMarker(lower, heading string) string {
	idx := strings.Index(lower, heading)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(lower[idx+len(heading):])
	return strings.TrimPrefix(rest, ":")
}

var sectionStopWords = []string{
	"objective", "boundaries", "constraints", "context", "evidence",
	"deliverable", "background", "notes", "scope",
}

func nextHeadingKeyword(lower string) string {
	for _, w := range sectionStopWords {
		if strings.HasPrefix(lower, w) {
			return w
		}
	}
	return ""
}

func isListLine(line string) bool {
	if line == "" {
		return false
	}
	r := []rune(line)[0]
	return r == '-' || r == '*' || r == '•' || (r >= '0' && r <= '9')
}

func acceptanceReminder(task, result string) string {
	if result == "" {
		return ""
	}
	if strings.Contains(result, acceptanceMarker) {
		return ""
	}
	items := extractAcceptanceCriteria(task)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(acceptanceMarker)
	b.WriteString("\nThe task contract for this sub-agent specified these acceptance criteria:\n")
	for i, c := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, c)
	}
	b.WriteString("Before treating this delegation as complete, check each criterion against the returned output evidence above - not against the builder's self-report. If a criterion is unmet or unverifiable from the output, follow up (send_message to the agent or a corrective task) instead of declaring done.")
	return b.String()
}
