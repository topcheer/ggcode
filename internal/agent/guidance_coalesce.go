package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/tool"
)

const (
	coalesceMaxHints = 1

	coalesceMaxSuppressedSummary = 3
)

var hintTagRe = regexp.MustCompile(`^\s*\[([A-Za-z][A-Za-z0-9 _:-]*?)\]`)

var criticalHintTags = map[string]bool{
	"CRITICAL":              true,
	"PERMISSION":            true,
	"IRREVERSIBLE":          true,
	"DESTRUCTIVE":           true,
	"SAFETY":                true,
	"SECURITY":              true,
	"BLOCKED":               true,
	"FATAL":                 true,
	"pre-commit-build-gate": true,
	"hardcoded-secret":      true,
	"path-traversal":        true,
	"git-destructive":       true,
	"non-atomic":            true,
}

func extractHintTag(hint string) string {
	m := hintTagRe.FindStringSubmatch(hint)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

var criticalHintTagsLower = func() map[string]bool {
	m := make(map[string]bool, len(criticalHintTags))
	for k := range criticalHintTags {
		m[strings.ToLower(k)] = true
	}
	return m
}()

func isCriticalTag(tag string) bool {
	if tag == "" {
		return false
	}
	return criticalHintTagsLower[strings.ToLower(tag)]
}

func coalesceGuidance(hints []string) []string {
	if len(hints) == 0 {
		return hints
	}
	if len(hints) <= coalesceMaxHints {

		deduped, dropped := dedupByTag(hints)
		return noteDedupDropped(deduped, dropped)
	}

	deduped, dropped := dedupByTag(hints)
	if len(deduped) <= coalesceMaxHints {
		return noteDedupDropped(deduped, dropped)
	}

	var critical, advisory []string
	for _, h := range deduped {
		tag := extractHintTag(h)
		if isCriticalTag(tag) {
			critical = append(critical, h)
		} else {
			advisory = append(advisory, h)
		}
	}

	if len(critical) >= coalesceMaxHints {
		result := critical[:coalesceMaxHints]
		return appendWithSuppression(result, critical[coalesceMaxHints:], advisory)
	}

	remaining := coalesceMaxHints - len(critical)
	result := make([]string, 0, coalesceMaxHints+1)
	result = append(result, critical...)

	var suppressedAdvisory []string
	if remaining > 0 && len(advisory) > remaining {
		result = append(result, advisory[:remaining]...)
		suppressedAdvisory = advisory[remaining:]
	} else if remaining > 0 {
		result = append(result, advisory...)
	}

	if len(suppressedAdvisory) > 0 {
		result = appendSuppressedSummary(result, suppressedAdvisory)
	}

	return result
}

func dedupByTag(hints []string) ([]string, int) {
	if len(hints) <= 1 {
		return hints, 0
	}

	seen := make(map[string]bool, len(hints))
	result := make([]string, 0, len(hints))
	dropped := 0

	for _, h := range hints {
		tag := extractHintTag(h)
		if tag != "" && seen[tag] {
			dropped++
			continue
		}
		if tag != "" {
			seen[tag] = true
		}
		result = append(result, h)
	}
	return result, dropped
}

func noteDedupDropped(result []string, dropped int) []string {
	if dropped <= 0 {
		return result
	}
	return append(result, fmt.Sprintf(
		"[%d additional hint(s) with duplicate tags were merged into the ones above]",
		dropped))
}

func appendWithSuppression(result, suppressedCritical, suppressedAdvisory []string) []string {
	all := append(suppressedCritical, suppressedAdvisory...)
	return appendSuppressedSummary(result, all)
}

func appendSuppressedSummary(result, suppressed []string) []string {
	if len(suppressed) == 0 {
		return result
	}

	var tags []string
	for i, h := range suppressed {
		if i >= coalesceMaxSuppressedSummary {
			break
		}
		tag := extractHintTag(h)
		if tag == "" {

			preview := strings.TrimSpace(h)
			if len(preview) > 30 {
				preview = preview[:30] + "..."
			}
			tags = append(tags, preview)
		} else {
			tags = append(tags, "["+tag+"]")
		}
	}

	extra := ""
	if len(suppressed) > coalesceMaxSuppressedSummary {
		extra = fmt.Sprintf(" (+%d more)", len(suppressed)-coalesceMaxSuppressedSummary)
	}

	summary := fmt.Sprintf(
		"[guidance-coalesced] %d additional guidance message(s) suppressed to prevent alert overload: %s%s",
		len(suppressed),
		strings.Join(tags, ", "),
		extra,
	)

	return append(result, summary)
}

func (a *Agent) applyToolResultGuidance(
	result *tool.Result,
	loopGuidance, searchParamHint, redundancyHint, equivHint, undoBlindHint string,
) {
	if loopGuidance == "" && searchParamHint == "" && redundancyHint == "" && equivHint == "" && undoBlindHint == "" {
		return
	}

	var hints []string
	if searchParamHint != "" {
		hints = append(hints, searchParamHint)
	}
	if undoBlindHint != "" {

		hints = append(hints, undoBlindHint)
	}
	if loopGuidance != "" {
		hints = append(hints, loopGuidance)
	}
	if redundancyHint != "" {
		hints = append(hints, redundancyHint)
	}
	if equivHint != "" {
		hints = append(hints, equivHint)
	}

	hints = coalesceGuidance(hints)

	injected := make([]string, 0, len(hints))
	for _, h := range hints {
		if a.guidanceBudget.allowDeduped(h) {
			injected = append(injected, h)
		}
	}

	scan := append(append([]string{}, a.guidanceBudget.delivered...), injected...)
	if ch := detectGuidanceConflict(stripCoalescedSummaries(scan)); ch != "" && a.guidanceBudget.allowDeduped(ch) {
		injected = append([]string{ch}, injected...)
	}

	if len(injected) == 0 {
		return
	}

	result.Content = result.Content + "\n\n" + strings.Join(injected, "\n\n")
}

func stripCoalescedSummaries(hints []string) []string {
	filtered := make([]string, 0, len(hints))
	for _, h := range hints {
		if strings.ToLower(extractHintTag(h)) == "guidance-coalesced" {
			continue
		}
		filtered = append(filtered, h)
	}
	return filtered
}
