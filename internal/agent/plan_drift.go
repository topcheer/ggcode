package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

// Plan Drift Detection - Spec-Driven Development Gap
//
// Research: Kiro (AWS spec-first IDE), GitHub Spec Kit, Claude Code plan mode,
// Cursor composer spec, Factory.ai all implement spec-driven development where
// a plan/spec is created BEFORE implementation. The critical gap these tools
// address: ensuring the agent actually follows its own plan.
//
// ggcode already has:
//   - enter_plan_mode / exit_plan_mode tools
//   - harness specparser.go (ParseSpec, ValidateSpec, SpecToTasks)
//   - task_create / task_update with dependencies
//   - fulfillment_gate.go (request-vs-work match)
//
// The gap: when exit_plan_mode presents a plan, the plan content is NEVER
// stored or tracked. The agent can silently ignore its own plan - implementing
// only partial items, skipping steps, or doing completely different work.
// No competitor (Claude Code, Cursor, Aider, Devin) performs plan-item-level
// reconciliation after plan exit.
//
// This gate fills that gap with a deterministic, zero-LLM-cost heuristic:
//  1. CAPTURE: When exit_plan_mode fires, extract plan items (bullet points,
//     numbered lists, heading-level tasks from the plan markdown)
//  2. KEYWORD EXTRACTION: For each plan item, extract distinctive keywords
//     (file names, function names, technology terms)
//  3. DRIFT CHECK: Before completion, verify that plan items have corresponding
//     work - files edited, tools called, or assistant text mentioning them
//  4. INJECT: If significant plan items are unaddressed, remind the agent
//
// The gate fires AT MOST ONCE per run (advisory, doesn't block completion).

const maxPlanDriftWarnings = 1

// planDriftState tracks the plan from exit_plan_mode and whether drift was checked.
type planDriftState struct {
	captured bool       // whether a plan was captured this run
	fired    bool       // whether the gate already fired
	items    []planItem // extracted plan items
}

type planItem struct {
	Text     string   // raw item text
	Keywords []string // distinctive keywords extracted from the item
}

func newPlanDriftState() *planDriftState {
	return &planDriftState{}
}

func (p *planDriftState) reset() {
	p.captured = false
	p.fired = false
	p.items = nil
}

// capturePlan extracts structured items from a plan presented via exit_plan_mode.
// It parses markdown bullet points, numbered lists, and heading-level tasks.
func (p *planDriftState) capturePlan(planContent string) {
	if strings.TrimSpace(planContent) == "" {
		return
	}

	p.items = p.items[:0]
	p.captured = true

	for _, line := range strings.Split(planContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		item := extractPlanItem(trimmed)
		if item.Text != "" && len(item.Keywords) > 0 {
			p.items = append(p.items, item)
		}
	}

	// Limit to reasonable number of items to avoid noise
	if len(p.items) > 30 {
		p.items = p.items[:30]
	}

	debug.Log("agent", "Plan drift: captured %d plan items", len(p.items))
}

// extractPlanItem determines if a line is a plan item and extracts keywords.
func extractPlanItem(line string) planItem {
	var text string

	// Bullet point: "- item" or "* item"
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		text = strings.TrimSpace(line[2:])
	}

	// Numbered list: "1. item" or "1) item"
	if text == "" {
		text = stripNumberedPrefix(line)
	}

	// Heading: "## item" or "### item" (treated as plan sections)
	if text == "" && strings.HasPrefix(line, "#") {
		hashEnd := 0
		for hashEnd < len(line) && line[hashEnd] == '#' {
			hashEnd++
		}
		rest := strings.TrimSpace(line[hashEnd:])
		// Only treat H2/H3 as items (H1 is usually the plan title)
		if hashEnd >= 2 && hashEnd <= 3 && rest != "" {
			text = rest
		}
	}

	if text == "" {
		return planItem{}
	}

	// Skip non-actionable lines (metadata, notes, etc.)
	lower := strings.ToLower(text)
	skipPrefixes := []string{"note:", "summary:", "overview", "background", "timeline", "estimated"}
	for _, sp := range skipPrefixes {
		if strings.HasPrefix(lower, sp) {
			return planItem{}
		}
	}

	keywords := extractKeywords(text)
	if len(keywords) == 0 {
		return planItem{}
	}

	return planItem{Text: text, Keywords: keywords}
}

// stripNumberedPrefix removes "N. " or "N) " prefix from a line.
func stripNumberedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) {
		return ""
	}
	rest := line[i:]
	if strings.HasPrefix(rest, ". ") || strings.HasPrefix(rest, ") ") {
		return strings.TrimSpace(rest[2:])
	}
	if rest == "." || rest == ")" {
		return ""
	}
	return ""
}

// extractKeywords pulls distinctive terms from plan item text.
// It looks for: file paths, code identifiers, quoted terms, and capitalized words.
func extractKeywords(text string) []string {
	var keywords []string
	seen := make(map[string]bool)

	words := strings.Fields(text)
	for _, w := range words {
		clean := strings.Trim(w, ",.;:()[]{}\"'`")
		if clean == "" {
			continue
		}

		// File paths with extensions
		if strings.Contains(clean, "/") && strings.Contains(clean, ".") {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
			continue
		}

		// File extensions alone (e.g., ".go", ".py")
		if strings.HasPrefix(clean, ".") && len(clean) > 1 && isAlpha(clean[1:]) {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
			continue
		}

		// CamelCase or snake_case identifiers (likely code symbols)
		if isCodeIdentifier(clean) && len(clean) >= 4 {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
		}
	}

	// Quoted terms: "foo bar" or `foo bar`
	extractQuoted(text, '"', &keywords, seen)
	extractQuoted(text, '\'', &keywords, seen)
	extractQuoted(text, '`', &keywords, seen)

	return keywords
}

// extractQuoted finds quoted substrings and adds them as keywords.
func extractQuoted(text string, quote byte, keywords *[]string, seen map[string]bool) {
	for {
		idx := strings.IndexByte(text, quote)
		if idx < 0 {
			break
		}
		rest := text[idx+1:]
		end := strings.IndexByte(rest, quote)
		if end < 0 {
			break
		}
		term := strings.TrimSpace(rest[:end])
		if len(term) >= 3 {
			lower := strings.ToLower(term)
			if !seen[lower] {
				*keywords = append(*keywords, lower)
				seen[lower] = true
			}
		}
		text = rest[end+1:]
	}
}

// isCodeIdentifier checks if a string looks like a code identifier.
func isCodeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	hasUpper := false
	hasUnder := false
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if r == '_' {
			hasUnder = true
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return hasUpper || hasUnder
}

// isAlpha checks if a string contains only ASCII letters.
func isAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

// checkPlanDrift verifies that plan items have corresponding work.
// Returns a non-empty message if significant drift is detected.
func (p *planDriftState) checkPlanDrift(runStats *RunStats, assistantText string) string {
	if !p.captured || p.fired || len(p.items) == 0 {
		return ""
	}

	p.fired = true

	// Build a corpus of "work done" from stats + assistant text
	workCorpus := strings.ToLower(assistantText)

	// Add edited file paths and names
	for _, f := range runStats.FilesEdited {
		workCorpus += " " + strings.ToLower(f)
		if idx := strings.LastIndex(f, "/"); idx >= 0 {
			workCorpus += " " + strings.ToLower(f[idx+1:])
		}
	}

	// Add tool names used
	for toolName := range runStats.ToolCalls {
		workCorpus += " " + strings.ToLower(toolName)
	}

	// Add commands run
	for _, cmd := range runStats.CommandsRun {
		workCorpus += " " + strings.ToLower(cmd)
	}

	// Check each plan item for keyword coverage
	var unaddressed []string
	for _, item := range p.items {
		if len(item.Keywords) == 0 {
			continue
		}

		matched := 0
		for _, kw := range item.Keywords {
			if strings.Contains(workCorpus, kw) {
				matched++
			}
		}

		threshold := (len(item.Keywords) + 1) / 2
		if matched < threshold {
			unaddressed = append(unaddressed, truncatePlanItem(item.Text, 80))
		}
	}

	if len(unaddressed) == 0 {
		return ""
	}

	totalItems := len(p.items)
	unaddrRatio := float64(len(unaddressed)) / float64(totalItems)
	if len(unaddressed) < 2 && unaddrRatio < 0.5 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Plan Drift Detection] ")
	if len(unaddressed) <= 3 {
		b.WriteString(fmt.Sprintf("%d of %d plan items appear unaddressed:\n", len(unaddressed), totalItems))
	} else {
		b.WriteString(fmt.Sprintf("%d of %d plan items appear unaddressed (showing first 3):\n", len(unaddressed), totalItems))
		unaddressed = unaddressed[:3]
	}
	for _, item := range unaddressed {
		b.WriteString(fmt.Sprintf("  - %s\n", item))
	}
	b.WriteString("\nReview these plan items and address any gaps before completing.")

	return b.String()
}

func truncatePlanItem(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// extractPlanFromArgs parses the "plan" field from exit_plan_mode tool arguments.
func extractPlanFromArgs(args json.RawMessage) string {
	var v struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return ""
	}
	return v.Plan
}
