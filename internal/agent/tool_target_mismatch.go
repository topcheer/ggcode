package agent

// Tool-Target Mismatch Detector
//
// Research basis:
//   - "Why AI Coding Agents Fail" (beginnersinai.org, 2026): intent-action
//     misalignment is a top failure mode -- the agent reasons about one target
//     (file, search term, path) but invokes a tool on a different target.
//   - SWE-bench analysis (2025): ~15% of agent failures involve the agent
//     operating on the wrong file or search term despite correct reasoning.
//   - CogCal-1 (2025): cognitive calibration gaps where stated intent and
//     executed action diverge indicate degraded planning fidelity.
//
// Problem: AI coding agents frequently state their intent in natural language
// ("Let me read the config file", "I'll search for the auth handler", "Let me
// edit internal/agent/agent.go") but then invoke a tool with a different target
// (reads a different file, searches for a different term). This mismatch is
// invisible to the user and silently sends the agent down the wrong path.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - symbol_grounding.go: checks hallucinated references in assistant text
//   - stale_read_detection.go: checks if re-read files have changed
//   - plan_abandon_detect.go: checks if declared steps were completed
//   None of these compare the agent's STATED TARGET to its ACTUAL tool target.
//
// Gap: No detector compares the natural-language intent ("I'll read X") against
// the actual tool arguments (path=X) to detect divergence. This detector fills
// that gap.
//
// Design:
//   - Scans the assistant text for intent statements mentioning specific file
//     paths or search terms ("I'll read internal/foo.go", "search for bar")
//   - Extracts the targets (paths, search patterns) from those statements
//   - Compares against the actual tool call arguments (file_path, pattern, path, etc.)
//   - When a mismatch is detected (stated target != actual target), injects
//     guidance to re-verify the correct target
//   - Zero LLM cost -- pure deterministic pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// toolTargetMaxWarnings: max warnings per run.
	toolTargetMaxWarnings = 2

	// toolTargetMinConfidence: minimum number of mismatch signals to trigger.
	toolTargetMinConfidence = 1
)

// toolTargetIntentRe detects intent statements that mention specific file paths
// or search targets. Matches patterns like:
//
//	"I'll read internal/agent/foo.go"
//	"Let me search for authenticationHandler"
//	"I'll edit src/main.go"
var toolTargetIntentRe = regexp.MustCompile(
	`(?i)(?:i(?:'ll|\s+will|\s+need\s+to)?\s+(?:read|edit|check|look\s+at|open|modify|update|examine|inspect|review|search|find|grep|scan)\s+(?:for\s+)?|let\s+me\s+(?:read|edit|check|look\s+at|open|modify|update|examine|inspect|review|search\s+for|find|grep|scan)\s+(?:for\s+)?)([^\s.,;:!?\n]+(?:/[^\s.,;:!?\n]+)*\.?[^\s.,;:!?\n]*)`,
)

// toolTargetSearchIntentRe detects search-specific intent with quoted terms.
var toolTargetSearchIntentRe = regexp.MustCompile(
	"(?i)(?:i(?:'ll|\\s+will|\\s+need\\s+to)?\\s+(?:search|grep|look\\s+for|find)\\s+for\\s+|let\\s+me\\s+(?:search|grep|look\\s+for|find)\\s+for\\s+)(?:[\"'`])([^\"'`]+)(?:[\"'`])",
)

// statedTarget represents a target mentioned in the agent's intent text.
type statedTarget struct {
	intentType string // "file", "search", "path"
	value      string
}

// toolTarget represents targets extracted from an actual tool call.
type toolTarget struct {
	toolName string
	targets  []string
}

// toolTargetState tracks mismatches across iterations.
type toolTargetState struct {
	warnings int
	curIter  int
}

// extractStatedTargets extracts file paths and search terms from assistant text
// intent statements. Returns a list of statedTarget pairs.
func extractStatedTargets(text string) []statedTarget {
	if len(text) == 0 {
		return nil
	}

	var targets []statedTarget
	seen := make(map[string]bool)

	// Extract path-like targets from intent statements
	matches := toolTargetIntentRe.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(m[1])
		if raw == "" || len(raw) < 3 {
			continue
		}
		// Filter out common false positives (articles, pronouns, etc.)
		lower := strings.ToLower(raw)
		if isFalsePositiveTarget(lower) {
			continue
		}
		// Normalize: strip trailing punctuation
		raw = strings.TrimRight(raw, ".,;:!?")
		if raw == "" {
			continue
		}
		key := lower
		if seen[key] {
			continue
		}
		seen[key] = true

		intentType := classifyIntent(m[0])
		targets = append(targets, statedTarget{intentType: intentType, value: raw})
	}

	// Extract quoted search terms
	searchMatches := toolTargetSearchIntentRe.FindAllStringSubmatch(text, -1)
	for _, m := range searchMatches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(m[1])
		if raw == "" || len(raw) < 2 {
			continue
		}
		lower := strings.ToLower(raw)
		key := "search:" + lower
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, statedTarget{intentType: "search", value: raw})
	}

	return targets
}

// isFalsePositiveTarget filters out non-target words that the regex might capture.
func isFalsePositiveTarget(s string) bool {
	falsePositives := map[string]bool{
		"the": true, "a": true, "an": true, "this": true, "that": true,
		"it": true, "them": true, "those": true, "these": true,
		"all": true, "any": true, "some": true, "each": true,
	}
	return falsePositives[s]
}

// classifyIntent determines the type of intent from the matched phrase.
func classifyIntent(phrase string) string {
	lower := strings.ToLower(phrase)
	if strings.Contains(lower, "search") || strings.Contains(lower, "grep") ||
		strings.Contains(lower, "find") || strings.Contains(lower, "look for") {
		return "search"
	}
	return "file"
}

// extractActualToolTargets extracts targets from actual tool call arguments.
func extractActualToolTargets(toolCalls []provider.ToolCallDelta) []toolTarget {
	var targets []toolTarget

	for _, tc := range toolCalls {
		if len(tc.Arguments) == 0 {
			continue
		}

		var args map[string]interface{}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			continue
		}

		tt := toolTarget{toolName: tc.Name}

		// Extract path-like arguments
		for _, key := range []string{"path", "file_path", "directory", "glob_pattern", "pattern"} {
			if v, ok := args[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					tt.targets = append(tt.targets, s)
				}
			}
		}

		// For edit tools, also extract old_text to compare against intent
		if v, ok := args["old_text"]; ok {
			if s, ok := v.(string); ok && len(s) > 10 {
				tt.targets = append(tt.targets, s)
			}
		}

		if len(tt.targets) > 0 {
			targets = append(targets, tt)
		}
	}

	return targets
}

// checkMismatch compares stated intent targets against actual tool call targets.
// Returns a guidance message if a mismatch is detected, "" otherwise.
func (s *toolTargetState) checkMismatch(statedTargets []statedTarget, actualTargets []toolTarget) string {
	if len(statedTargets) == 0 || len(actualTargets) == 0 {
		return ""
	}

	// Build a set of all actual target strings (normalized)
	actualSet := make(map[string]bool)
	for _, at := range actualTargets {
		for _, t := range at.targets {
			normal := normalizePathTarget(t)
			if normal != "" {
				actualSet[normal] = true
			}
		}
	}

	var mismatches []statedTarget
	for _, st := range statedTargets {
		normalStated := normalizePathTarget(st.value)
		if normalStated == "" {
			continue
		}

		found := false
		for actual := range actualSet {
			if targetsMatch(normalStated, actual) {
				found = true
				break
			}
		}
		if !found {
			mismatches = append(mismatches, st)
		}
	}

	if len(mismatches) < toolTargetMinConfidence {
		return ""
	}

	var parts []string
	for _, m := range mismatches {
		if len(parts) >= 3 {
			break
		}
		excerpt := m.value
		if len(excerpt) > 60 {
			excerpt = excerpt[:60] + "..."
		}
		parts = append(parts, excerpt)
	}

	return fmt.Sprintf(
		"[intent-action mismatch] Your stated target(s) (%s) do not match the "+
			"actual tool call target(s). Verify you are operating on the correct "+
			"file/path/search term before proceeding. Intent-action misalignment is "+
			"a common source of agent errors.",
		strings.Join(parts, ", "),
	)
}

// normalizePathTarget normalizes a target string for comparison.
func normalizePathTarget(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`")
	s = strings.ToLower(s)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// targetsMatch checks if a stated target matches an actual target.
func targetsMatch(stated, actual string) bool {
	if stated == actual {
		return true
	}
	// Check basename match
	statedBase := stated
	if idx := strings.LastIndex(stated, "/"); idx >= 0 {
		statedBase = stated[idx+1:]
	}
	actualBase := actual
	if idx := strings.LastIndex(actual, "/"); idx >= 0 {
		actualBase = actual[idx+1:]
	}
	if statedBase != "" && statedBase == actualBase {
		return true
	}
	// Substring containment with path-component boundary check.
	// The match must occur at a path separator (/, _, ., -) or string
	// boundary to avoid false matches like "log.go" matching "dialog.go".
	if len(stated) >= 4 && boundaryContains(actual, stated) {
		return true
	}
	if len(actual) >= 4 && boundaryContains(stated, actual) {
		return true
	}
	return false
}

// boundaryContains checks if needle appears in haystack at a word/path boundary.
// A valid match must be preceded by start-of-string or a delimiter (/, _, ., -)
// and followed by end-of-string or a delimiter. This prevents "log.go" matching
// "dialog.go" while still allowing "agent" to match "internal/agent/agent.go".
func boundaryContains(haystack, needle string) bool {
	searchStart := 0
	for {
		idx := strings.Index(haystack[searchStart:], needle)
		if idx < 0 {
			return false
		}
		idx += searchStart
		beforeOK := idx == 0 || isPathDelimiter(haystack[idx-1])
		afterIdx := idx + len(needle)
		afterOK := afterIdx >= len(haystack) || isPathDelimiter(haystack[afterIdx])
		if beforeOK && afterOK {
			return true
		}
		searchStart = idx + 1
	}
}

func isPathDelimiter(ch byte) bool {
	return ch == '/' || ch == '_' || ch == '.' || ch == '-'
}

// maybeWarnToolTargetMismatch is the entry point called from the agent loop.
func (a *Agent) maybeWarnToolTargetMismatch(assistantText string, toolCalls []provider.ToolCallDelta) string {
	if a.toolTargetMismatch == nil {
		return ""
	}
	if a.toolTargetMismatch.warnings >= toolTargetMaxWarnings {
		return ""
	}

	statedTargets := extractStatedTargets(assistantText)
	if len(statedTargets) == 0 {
		return ""
	}

	actualTargets := extractActualToolTargets(toolCalls)
	if len(actualTargets) == 0 {
		return ""
	}

	hint := a.toolTargetMismatch.checkMismatch(statedTargets, actualTargets)
	if hint != "" {
		a.toolTargetMismatch.warnings++
	}
	return hint
}
