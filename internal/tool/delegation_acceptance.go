package tool

import (
	"fmt"
	"strings"
)

// r384 builder-validator separation (arXiv 2510.10460: 75.3% of multi-agent
// code-generation failures stem from planner-coder handoff drift; MAST
// arXiv 2503.13657: explicit verification phases add +15.6% task success).
// ggcode's delegation loop had the contract side (spawn_agent task schema
// coaches acceptance criteria into the task text) but no validator side:
// wait_agent returned the sub-agent's self-report and the parent LLM
// typically consumed it as-is. These helpers extract the acceptance
// criteria the ORIGINATOR wrote into the task and append a checkpoint
// reminder so the parent validates the returned evidence against the
// original contract - not against the builder's own summary.

// acceptanceHeadings are section markers whose following lines are
// acceptance criteria. Matched case-insensitively at line start, with or
// without markdown heading/leading whitespace.
var acceptanceHeadings = []string{
	"acceptance criteria",
	"acceptance",
	"success criteria",
	"definition of done",
	"done when",
	"done:",
}

// maxAcceptanceItems caps extraction so a pathological task cannot blow up
// the tool result.
const maxAcceptanceItems = 10

// maxAcceptanceItemLen trims each criterion to keep the reminder compact.
const maxAcceptanceItemLen = 200

// acceptanceMarker lets consumers detect that a reminder was already
// appended (re-polls of a completed run stay idempotent).
const acceptanceMarker = "=== ACCEPTANCE CHECKPOINT (delegation validation) ==="

// extractAcceptanceCriteria pulls the acceptance-criteria items the task
// author wrote into the task text. Supports markdown headings ("##
// Acceptance Criteria") and plain-line markers ("Done when:"), bullet or
// numbered lists under them, and stops at the next section heading.
// Returns nil when the task carries no recognizable criteria section.
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
					// Heading itself may inline the first criterion:
					// "done: tests pass" - take what follows the colon.
					if rest := textAfterMarker(lower, h); rest != "" {
						items = appendCriterion(items, line)
					}
					break
				}
			}
			continue
		}

		// Inside the criteria section: stop at the next heading/section.
		if line == "" {
			continue // blank lines between bullets are tolerated
		}
		if strings.HasPrefix(line, "#") {
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

// appendCriterion normalizes a bullet/numbered/plain line into a criterion
// string, enforcing the item cap and per-item length trim.
func appendCriterion(items []string, line string) []string {
	if len(items) >= maxAcceptanceItems {
		return items
	}
	c := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*•0123456789."))
	if c == "" {
		return items
	}
	if len(c) > maxAcceptanceItemLen {
		c = c[:maxAcceptanceItemLen] + "..."
	}
	return append(items, c)
}

// textAfterMarker returns the text following "heading:" inline, if any.
func textAfterMarker(lower, heading string) string {
	idx := strings.Index(lower, heading)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(lower[idx+len(heading):])
	return strings.TrimPrefix(rest, ":")
}

// sectionStopWords end the criteria block when seen at a line start (the
// task schema coaches objective/boundaries/constraints/acceptance sections).
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

// acceptanceReminder builds the checkpoint block appended to a COMPLETED
// delegation result. Empty string when there is nothing to remind about
// (no criteria in the task, or no result text).
func acceptanceReminder(task, result string) string {
	if result == "" {
		return ""
	}
	if strings.Contains(result, acceptanceMarker) {
		return "" // already reminded (re-poll)
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
