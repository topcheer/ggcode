package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Request Fulfillment Gate — Pre-Completion Coverage Verification
//
// Research basis: Claude Code, Cursor, and Aider all implement some form of
// completion verification before the agent declares a task done. The pattern:
//   - Claude Code: "check your work" system prompt + auto-verify loop
//   - Cursor: inline diff review + "did you complete the task?" meta-check
//   - Aider: explicit diff display + user confirmation before commit
//   - Devin: SICA overseer verifies goal completion before stopping
//
// The gap in ggcode: the agent loop has build verification (verify.go), todo
// completion checks (todo_check.go), and convergence pressure (iteration
// budget warnings). But NONE of these verify that the agent's actual WORK
// matches the USER'S REQUEST. The agent can silently finish after:
//   - Only implementing 2 of 3 requested features (no todos created)
//   - Reading/exploring code but never making changes when changes were expected
//   - Making edits to the wrong files (not matching the request's intent)
//
// This gate fills that gap with a deterministic, zero-LLM-cost heuristic:
//   1. ACTION DETECTION: analyze the user's request for actionable intent
//      (fix, add, create, implement, refactor, delete, etc.)
//   2. PRODUCTIVITY CHECK: verify the agent performed matching actions
//      (files edited matching request keywords, commands run, etc.)
//   3. KEYWORD OVERLAP: check if files the agent edited relate to the
//      files/concepts mentioned in the user's request
//   4. MULTI-PART DETECTION: if the request has multiple action items
//      ("add X and Y", "fix A, then update B"), check coverage
//
// The gate fires AT MOST ONCE per run, injecting a reminder message that
// gives the agent a final chance to address gaps before returning.

const maxFulfillmentGateWarnings = 1

// actionVerbs are verbs that indicate the user expects concrete code changes.
var actionVerbs = []string{
	"add", "create", "implement", "fix", "repair", "build",
	"refactor", "update", "modify", "change", "remove", "delete",
	"write", "generate", "convert", "migrate", "optimize", "extract",
	"rename", "replace", "insert", "move", "split", "merge",
}

// questionIndicators suggest the request is informational, not actionable.
var questionIndicators = []string{
	"what ", "why ", "how does", "how do", "explain", "is there",
	"are there", "can you tell", "show me", "list all", "where is",
	"where are", "find ", "search for", "describe",
}

// fulfillmentGateState tracks whether the gate has already fired this run.
type fulfillmentGateState struct {
	fired bool
}

func newFulfillmentGateState() *fulfillmentGateState {
	return &fulfillmentGateState{}
}

func (f *fulfillmentGateState) reset() {
	f.fired = false
}

// checkFulfillmentGate runs at the "about to return" exit point to verify
// the agent's work matches the user's request. Returns a non-empty message
// if a gap is detected that warrants a final reminder.
//
// Parameters:
//   - userPrompt: the original user message text
//   - runStats: accumulated stats from the run (tool calls, files edited, etc.)
//   - assistantText: the agent's final response text
//
// Returns "" when:
//   - the gate already fired this run
//   - the request appears informational (no action expected)
//   - the agent made productive changes matching the request
//   - todos exist and were checked separately
func (a *Agent) checkFulfillmentGate(userPrompt string, runStats *RunStats, assistantText string) string {
	if a.fulfillmentGate.fired {
		return ""
	}

	prompt := strings.ToLower(userPrompt)
	if len(strings.TrimSpace(prompt)) < 10 {
		return ""
	}

	// If the request is primarily a question, skip the gate.
	if isInformationalRequest(prompt) {
		debug.Log("fulfillment-gate", "request appears informational, skipping")
		return ""
	}

	// Detect action verbs in the request.
	actions := detectActions(prompt)
	if len(actions) == 0 {
		debug.Log("fulfillment-gate", "no action verbs detected in request, skipping")
		return ""
	}

	// Extract significant keywords from the request (file names, function names, etc.)
	requestKeywords := extractRequestKeywords(prompt)
	if len(requestKeywords) == 0 {
		// Can't determine coverage without keywords — skip.
		debug.Log("fulfillment-gate", "no extractable keywords in request, skipping")
		return ""
	}

	// Check what the agent actually did.
	filesEdited := runStats.FilesEdited
	hasEdits := len(filesEdited) > 0
	hasCommands := len(runStats.CommandsRun) > 0

	// Case 1: Action requested but no files edited and no commands run.
	// The agent explored but didn't make changes.
	if !hasEdits && !hasCommands && len(runStats.ToolCalls) > 0 {
		a.fulfillmentGate.fired = true
		debug.Log("fulfillment-gate", "action requested but no edits or commands — injecting reminder")
		return fmt.Sprintf(
			"Before finishing: your request included action verbs (%s) but no files were edited and no commands were run. "+
				"If the user asked you to make changes, verify you have completed all requested modifications. "+
				"If you determined no changes are needed, clearly explain why in your response.",
			strings.Join(actions[:min(3, len(actions))], ", "),
		)
	}

	// Case 2: Files edited but keyword overlap is low — may have edited
	// unrelated files. Only trigger when the request mentions specific files/concepts.
	if hasEdits && len(requestKeywords) >= 1 {
		matched := countFileKeywordMatches(requestKeywords, filesEdited)
		// If none of the request keywords appear in any edited file path,
		// it's possible the agent edited the wrong files.
		if matched == 0 && len(filesEdited) <= 5 {
			a.fulfillmentGate.fired = true
			debug.Log("fulfillment-gate", "edited files have no keyword overlap with request — injecting reminder")
			return fmt.Sprintf(
				"Before finishing: you edited %d file(s) but none match the key terms in the user's request (%s). "+
					"Verify you addressed the correct files and that all requested changes are complete.",
				len(filesEdited), strings.Join(requestKeywords[:min(3, len(requestKeywords))], ", "),
			)
		}
	}

	// Case 3: Multi-part request — check if all parts seem addressed.
	// We detect multi-part via conjunctions and list patterns.
	parts := detectMultiPart(prompt)
	if parts >= 2 && hasEdits {
		// Heuristic: if the request has N parts, expect at least ceil(N/2)
		// distinct files edited (some parts may target the same file).
		expected := (parts + 1) / 2
		if expected < 1 {
			expected = 1
		}
		if len(filesEdited) < expected {
			a.fulfillmentGate.fired = true
			debug.Log("fulfillment-gate", "multi-part request (%d parts) but only %d files edited — injecting reminder", parts, len(filesEdited))
			return fmt.Sprintf(
				"Before finishing: the user's request appears to have %d distinct parts. "+
					"Verify each part was addressed — only %d file(s) were edited. "+
					"If some parts were addressed within the same file or don't require code changes, this is fine.",
				parts, len(filesEdited),
			)
		}
	}

	debug.Log("fulfillment-gate", "passed: actions=%d keywords=%d files=%d parts=%d",
		len(actions), len(requestKeywords), len(filesEdited), parts)
	return ""
}

// isInformationalRequest returns true if the prompt looks like a question
// rather than an action request.
func isInformationalRequest(prompt string) bool {
	questionCount := 0
	for _, indicator := range questionIndicators {
		if strings.Contains(prompt, indicator) {
			questionCount++
		}
	}
	if questionCount == 0 {
		return false
	}
	// Only classify as informational if there are NO action verbs,
	// or questions heavily outnumber actions.
	actionCount := 0
	for _, verb := range actionVerbs {
		if strings.Contains(prompt, verb) {
			actionCount++
		}
	}
	return actionCount == 0 || questionCount > actionCount
}

// detectActions returns the action verbs found in the prompt.
func detectActions(prompt string) []string {
	var found []string
	seen := make(map[string]bool)
	for _, verb := range actionVerbs {
		if strings.Contains(prompt, verb) && !seen[verb] {
			found = append(found, verb)
			seen[verb] = true
		}
	}
	return found
}

// extractRequestKeywords pulls significant terms from the request that are
// likely file names, function names, or package names.
func extractRequestKeywords(prompt string) []string {
	var keywords []string
	seen := make(map[string]bool)

	// Match file paths: foo/bar.go, foo/baz.py
	words := strings.Fields(prompt)
	for _, word := range words {
		clean := strings.Trim(word, "`\"'()[]{},.;:!?")
		clean = strings.ToLower(clean)

		// File with extension
		if strings.Contains(clean, ".") && len(clean) > 3 {
			parts := strings.Split(clean, "/")
			name := parts[len(parts)-1]
			if !seen[name] && len(name) > 3 {
				keywords = append(keywords, name)
				seen[name] = true
			}
		}

		// CamelCase / snake_case identifiers (likely function/type names)
		if isIdentifier(clean) && len(clean) > 4 && !isStopWord(clean) {
			if !seen[clean] {
				keywords = append(keywords, clean)
				seen[clean] = true
			}
		}
	}
	return keywords
}

// countFileKeywordMatches counts how many request keywords appear in any edited
// file path.
func countFileKeywordMatches(keywords, files []string) int {
	matched := 0
	for _, kw := range keywords {
		for _, f := range files {
			if strings.Contains(strings.ToLower(f), kw) {
				matched++
				break
			}
		}
	}
	return matched
}

// detectMultiPart returns the estimated number of distinct action items in
// the request, based on conjunction patterns and list formatting.
func detectMultiPart(prompt string) int {
	count := 1 // at least one part

	// Conjunctions that separate distinct tasks
	conjunctions := []string{" and ", " then ", " also ", " after that "}
	for _, conj := range conjunctions {
		if strings.Contains(prompt, conj) {
			count++
		}
	}

	// Numbered/bulleted lists: "1. ", "2. ", "- ", "* "
	for i := 2; i <= 9; i++ {
		marker := fmt.Sprintf("%d. ", i)
		if strings.Contains(prompt, marker) {
			count++
		}
	}
	if strings.Contains(prompt, "- ") || strings.Contains(prompt, "* ") {
		count++
	}

	// Multiple action verbs also suggest multiple parts
	actions := detectActions(prompt)
	if len(actions) > count {
		count = len(actions)
	}

	if count > 5 {
		count = 5 // cap to avoid false positives from wordy prompts
	}
	return count
}

// isIdentifier checks if a string looks like a code identifier.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// isStopWord returns true for common words that aren't meaningful identifiers.
func isStopWord(s string) bool {
	stopWords := map[string]bool{
		"the": true, "this": true, "that": true, "with": true, "from": true,
		"have": true, "should": true, "would": true, "could": true,
		"there": true, "their": true, "about": true, "which": true,
		"when": true, "where": true, "what": true, "these": true,
		"those": true, "being": true, "having": true, "using": true,
		"after": true, "before": true, "needs": true, "need": true,
		"make": true, "sure": true, "into": true, "some": true,
		"will": true, "must": true, "does": true, "doesn": true,
		"don": true, "isn": true, "wasn": true, "aren": true,
		"they": true, "them": true, "your": true, "code": true,
		"file": true, "files": true, "test": true, "tests": true,
		"function": true, "method": true, "class": true, "struct": true,
		"package": true, "module": true, "error": true, "handle": true,
		"system": true, "here": true, "please": true, "check": true,
	}
	return stopWords[s]
}
