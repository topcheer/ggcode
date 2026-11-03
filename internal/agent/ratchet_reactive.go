package agent

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

func (rs *RuleStore) MatchingRulesForResult(resultContent, toolArgs string) []Rule {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	cleaned := stripHarnessAdvisories(resultContent)

	var result []Rule
	for _, r := range rs.rules {
		if r.MatchPattern == "" {
			continue
		}

		if len(r.MatchPattern) < minMatchPatternLen {
			continue
		}
		re, err := regexp.Compile(r.MatchPattern)
		if err != nil {
			continue
		}
		if !re.MatchString(cleaned) {
			continue
		}

		if r.ToolPattern != "" {
			tre, terr := regexp.Compile(r.ToolPattern)
			if terr != nil || !tre.MatchString(toolArgs) {
				continue
			}
		}
		result = append(result, r)
	}

	if len(result) > 0 {
		debug.Log("ratchet", "reactive match: %d rules matched result content", len(result))
	}

	return result
}

const minMatchPatternLen = 8

var harnessAdvisoryPrefixes = []string{
	"[rules — learned from past mistakes]",
	"[verify]",
	"[truncation advisory",
	"[full original output",
	"destructive git operation detected",

	"critical: [",
	"safer alternative:",
	"if this operation is intentional",
	"security escalation:",
	"untrusted tool output source:",
}

func stripHarnessAdvisories(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	prevDropped := false
	for _, line := range lines {
		trimmed := strings.ToLower(strings.TrimSpace(line))
		drop := false
		for _, p := range harnessAdvisoryPrefixes {
			if strings.HasPrefix(trimmed, p) {
				drop = true
				break
			}
		}
		if !drop && (strings.HasPrefix(trimmed, "\u26a0") || strings.HasPrefix(trimmed, "\u2192")) {
			drop = true
		}

		if !drop && prevDropped && line != strings.TrimLeft(line, " \t") {
			drop = true
		}
		prevDropped = drop
		if drop {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

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
