package agent

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// MatchingRulesForResult finds ratchet rules whose MatchPattern matches the
// tool RESULT content (error output). This is the reactive path: when a tool
// result contains a known error pattern, we immediately inject the rule's
// FixHint so the agent can correct itself in the NEXT iteration, instead of
// waiting for the generic error-streak threshold.
//
// This complements MatchingRulesForTool which matches proactively against
// tool ARGS before the tool runs. Both paths can fire for the same tool call.
//
// Research basis: SICA's "learned rules as first-class citizens" pattern
// (Robeyns et al. 2025, arXiv:2504.15228) — rules learned from past failures
// should be available not just at run start, but reactively when the agent
// encounters the same error class again.
func (rs *RuleStore) MatchingRulesForResult(resultContent string) []Rule {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	// Quick exit: only check if the result looks like an error
	// (case-insensitive check for common error markers)
	lowerResult := strings.ToLower(resultContent)
	hasErrorMarker := strings.Contains(lowerResult, "error") ||
		strings.Contains(lowerResult, "fail") ||
		strings.Contains(lowerResult, "undefined") ||
		strings.Contains(lowerResult, "cannot find") ||
		strings.Contains(lowerResult, "panic") ||
		strings.Contains(lowerResult, "fatal") ||
		strings.Contains(lowerResult, "not found") ||
		strings.Contains(lowerResult, "denied") ||
		strings.Contains(lowerResult, "refused")

	if !hasErrorMarker {
		return nil
	}

	var result []Rule
	for _, r := range rs.rules {
		if r.MatchPattern == "" {
			continue
		}
		re, err := regexp.Compile(r.MatchPattern)
		if err != nil {
			continue
		}
		if re.MatchString(resultContent) {
			result = append(result, r)
		}
	}

	if len(result) > 0 {
		debug.Log("ratchet", "reactive match: %d rules matched result content", len(result))
	}

	return result
}

// mergeRuleSets combines preventively-matched rules (from tool args) and
// reactively-matched rules (from result content), deduplicating by rule ID.
// Preventive rules come first (they fire before the error), reactive rules
// second (they fire because the error already occurred).
func mergeRuleSets(preventive, reactive []Rule) []Rule {
	seen := make(map[string]bool, len(preventive)+len(reactive))
	var merged []Rule

	for _, r := range preventive {
		if !seen[r.ID] {
			seen[r.ID] = true
			merged = append(merged, r)
		}
	}
	for _, r := range reactive {
		if !seen[r.ID] {
			seen[r.ID] = true
			merged = append(merged, r)
		}
	}

	return merged
}
