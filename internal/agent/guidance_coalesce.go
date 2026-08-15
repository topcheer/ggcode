package agent

// Guidance Coalescer - Alert Overload Suppressor
//
// Research basis:
//   - arXiv:2506.05109 "Truly Self-Improving Agents Require Intrinsic
//     Metacognitive Learning" (2025): effective self-improvement requires
//     intrinsic metacognitive monitoring - the agent must be aware of its
//     own cognitive guidance stream, not just the environment. When too
//     many meta-guidance messages fire simultaneously, the agent's
//     metacognitive bandwidth is overwhelmed, reducing the effectiveness
//     of each individual message.
//   - Alert fatigue in observability (Google SRE Book Ch.6, 2025 update):
//     when humans (and LLMs) receive too many simultaneous alerts, each
//     alert's marginal informational value drops to near-zero. The system
//     enters a "cry wolf" state where all alerts are ignored.
//   - ACE Framework (ICLR 2026): identifies "context collision" as a
//     primary trajectory failure mode - when 3+ guidance directives
//     arrive in the same turn, the model cannot prioritize among them,
//     leading to partial compliance or conflicting actions.
//   - Anthropic "Context Engineering" (Sep 2025): every token of guidance
//     competes for attention budget. Stacking 5+ advisory messages in a
//     single tool result dilutes the signal of the most important one.
//
// Problem: ggcode has 37+ independent detectors (maybeWarn*) that each
// generate guidance text appended to tool results. In a single iteration,
// multiple detectors can fire simultaneously:
//
//	[STRATEGY-STAGNATION] web_search has failed 2 consecutive times...
//	[Silent Error Warning] You have 3 unaddressed tool errors...
//	[Tool Call Storm] 4 consecutive tool calls detected...
//	[Analysis Paralysis] 9 exploration calls detected...
//	[Tool Diversity Alert] 8 of your last 10 tool calls...
//	[Context Pressure: CRITICAL] Context window is 63% full...
//
// When 5+ of these stack into one tool result, the model:
//  1. Cannot determine which directive is most important
//  2. May receive contradictory guidance (one says "explore more",
//     another says "act now")
//  3. Spends tokens processing advisory overhead instead of acting
//  4. Eventually learns to ignore all guidance ("alert fatigue")
//
// Gap: No mechanism coalesces, deduplicates, or prioritizes guidance
// hints before injection. Each detector independently appends its message
// to the hints slice with no awareness of what else is being injected.
//
// Design:
//   - Extracts the tag ([TAG-NAME]) from each hint for deduplication
//   - Deduplicates by tag (keeps first occurrence of each tag)
//   - Applies per-result cap (default 3) to prevent overload
//   - Prioritizes critical tags (errors, safety) over advisory tags
//   - When truncating, appends a brief summary of suppressed hints
//   - Zero LLM cost - pure deterministic text processing

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/tool"
)

const (
	// coalesceMaxHints: maximum hints to inject per tool result.
	// Reduced from 3 to 1 - each hint wastes 100-300 tokens of context.
	// Only the single most critical hint should be surfaced.
	coalesceMaxHints = 1

	// coalesceMaxSuppressedSummary: max suppressed tag names to list.
	coalesceMaxSuppressedSummary = 3
)

// hintTagRe extracts the leading [TAG] from a guidance hint.
var hintTagRe = regexp.MustCompile(`^\s*\[([A-Za-z][A-Za-z0-9 _:-]*?)\]`)

// criticalHintTags are always retained regardless of cap. These represent
// immediate correctness or safety issues that must not be suppressed.
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
}

// extractHintTag returns the bracketed tag at the start of a hint, or ""
// if no tag is found.
func extractHintTag(hint string) string {
	m := hintTagRe.FindStringSubmatch(hint)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// isCriticalTag returns true if the tag represents a critical/safety issue
// that should always be retained. #441: EXACT match only (case-insensitive)
// — the old substring fallback let tags like "[SECURITY-TIP]" or
// "[BLOCKED-FOR-NOW]" inherit permanent budget exemption just by naming.
func isCriticalTag(tag string) bool {
	if tag == "" {
		return false
	}
	if criticalHintTags[tag] {
		return true
	}
	return criticalHintTags[strings.ToUpper(tag)]
}

// coalesceGuidance takes a slice of guidance hints for a single tool
// result, deduplicates by tag, applies priority ordering, and caps the
// total to prevent alert overload.
//
// The algorithm:
//  1. Extract tag from each hint.
//  2. Deduplicate by tag (keep first occurrence).
//  3. Separate critical hints from advisory hints.
//  4. If total exceeds cap, keep all critical + top advisory hints.
//  5. If hints are suppressed, append a brief summary.
func coalesceGuidance(hints []string) []string {
	if len(hints) == 0 {
		return hints
	}
	if len(hints) <= coalesceMaxHints {
		// Even with few hints, deduplicate by tag.
		return dedupByTag(hints)
	}

	// Deduplicate first.
	deduped := dedupByTag(hints)
	if len(deduped) <= coalesceMaxHints {
		return deduped
	}

	// Separate critical from advisory.
	var critical, advisory []string
	for _, h := range deduped {
		tag := extractHintTag(h)
		if isCriticalTag(tag) {
			critical = append(critical, h)
		} else {
			advisory = append(advisory, h)
		}
	}

	// If critical alone exceeds cap, keep first N critical.
	if len(critical) >= coalesceMaxHints {
		result := critical[:coalesceMaxHints]
		return appendWithSuppression(result, critical[coalesceMaxHints:], advisory)
	}

	// Fill remaining slots with advisory hints.
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

// dedupByTag removes hints with duplicate tags, keeping the first
// occurrence of each tag.
func dedupByTag(hints []string) []string {
	if len(hints) <= 1 {
		return hints
	}

	seen := make(map[string]bool, len(hints))
	result := make([]string, 0, len(hints))

	for _, h := range hints {
		tag := extractHintTag(h)
		if tag != "" && seen[tag] {
			continue
		}
		if tag != "" {
			seen[tag] = true
		}
		result = append(result, h)
	}

	return result
}

// appendWithSuppression adds a suppression summary when critical hints
// themselves are truncated.
func appendWithSuppression(result, suppressedCritical, suppressedAdvisory []string) []string {
	all := append(suppressedCritical, suppressedAdvisory...)
	return appendSuppressedSummary(result, all)
}

// appendSuppressedSummary adds a brief note about which hints were
// suppressed to prevent alert overload.
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
			// Keep first 30 chars of untagged hint.
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

// applyToolResultGuidance assembles guidance hints from various detectors,
// coalesces them, records tags for cross-session promotion, and appends the
// result to the tool output content. Both vision and non-vision tool result
// paths share this method to avoid divergent hint-assembly logic.
//
// Parameters are individual hint strings (empty = not applicable):
//   - loopGuidance: sequence pattern guidance (e.g., strategy-stagnation)
//   - searchParamHint: search parameter optimization guidance
//   - redundancyHint: tool result redundancy guidance
//   - equivHint: equivalent tool call guidance
//   - overuseHint: tool overuse guidance
func (a *Agent) applyToolResultGuidance(
	result *tool.Result,
	loopGuidance, searchParamHint, redundancyHint, equivHint, overuseHint string,
) {
	if loopGuidance == "" && searchParamHint == "" && redundancyHint == "" && equivHint == "" && overuseHint == "" {
		return
	}

	var hints []string
	if searchParamHint != "" {
		hints = append(hints, searchParamHint)
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
	if overuseHint != "" {
		hints = append(hints, overuseHint)
	}

	hints = coalesceGuidance(hints)
	if ch := detectGuidanceConflict(hints); ch != "" {
		hints = append([]string{ch}, hints...)
	}

	for _, h := range hints {
		if tag := extractHintTag(h); tag != "" {
			a.guidancePromoter.RecordTag(tag)
		}
	}

	result.Content = result.Content + "\n\n" + strings.Join(hints, "\n\n")
}
