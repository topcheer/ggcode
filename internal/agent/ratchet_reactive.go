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
// Domain coupling (#4293): rules that declare an explicit ToolPattern
// ("what command shape triggers this rule") must ALSO match the tool args on
// the reactive path. Without this, an output-only match injects command-
// shaped advice for commands that never had that shape — e.g. a "never run
// rm -rf" rule firing on a git command whose output merely quoted the
// harness's own destructive-operation banner ("rm -rf recursively force-
// deletes ..."). Output-only matching stays available for rules without a
// ToolPattern (pure error-shape rules like "cannot find package olm").
//
// Harness advisory blocks ([Rules ...], [Verify ...], DESTRUCTIVE ... banners)
// are stripped before matching: those blocks quote danger keywords while
// explaining them, and matching against them is self-referential — the safety
// wrapper's own wording feeds the very rules that warn about that wording.
//
// Research basis: SICA's "learned rules as first-class citizens" pattern
// (Robeyns et al. 2025, arXiv:2504.15228) — rules learned from past failures
// should be available not just at run start, but reactively when the agent
// encounters the same error class again.
func (rs *RuleStore) MatchingRulesForResult(resultContent, toolArgs string) []Rule {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	// Match against the payload, not the advisory wrapping.
	cleaned := stripHarnessAdvisories(resultContent)

	// Quick exit: only check if the result looks like an error
	// (case-insensitive check for common error markers)
	lowerResult := strings.ToLower(cleaned)
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
		// Quality floor: LLM-learned patterns below this size are keyword
		// fragments ("force", "rm ") that match unrelated prose far more
		// often than the error class they were learned from.
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
		// #4293 domain coupling: command-shaped rules must see the same
		// shape in the args that actually ran.
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

// minMatchPatternLen is the reactive-path quality floor for learned
// MatchPatterns. Patterns shorter than this cannot describe an error shape
// precisely enough to be worth matching against arbitrary output.
const minMatchPatternLen = 8

// harnessAdvisoryPrefixes are fixed strings the harness prepends to tool
// results (verification verdicts, rule injections, safety banners). They quote
// danger keywords while explaining them, so they must not participate in
// rule matching — otherwise the wrapper's own wording triggers the rules.
var harnessAdvisoryPrefixes = []string{
	"[rules — learned from past mistakes]",
	"[verify]",
	"[truncation advisory",
	"[full original output",
	"destructive git operation detected",
	// #4391: harness CRITICAL banners always carry a bracketed tag
	// ("CRITICAL: [discard_all]") - anchoring on the bracket keeps a
	// linter's bare "Critical: severity high" payload line intact.
	"critical: [",
	"safer alternative:",
	"if this operation is intentional",
	"security escalation:",
	"untrusted tool output source:",
}

// stripHarnessAdvisories drops harness-generated advisory lines from a tool
// result so reactive rule matching sees only the actual command payload and
// its real output. Line-granularity is deliberate: banners are line-structured
// and the payload lines in between (STDERR, exit codes) stay intact.
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
		// #4391: an indented line right after a dropped advisory line is
		// that banner's continuation ("  Risk: ...", "  Safer alternative:
		// ..." under DESTRUCTIVE/CRITICAL banners). It belongs to the
		// harness, not the payload. The bare "risk:" prefix was removed so
		// audit-tool "Risk: high" output stays matchable; this rule covers
		// the banner continuation lines instead.
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
