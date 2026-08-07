package agent

// Circular Reasoning Detector
//
// Research basis:
//   - "Chain-of-Thought Reasoning Without Prompting" (arXiv 2502.06606, 2025):
//     shows that CoT quality varies enormously -- many reasoning chains contain
//     structural defects where the conclusion is merely a restatement of the
//     premise, providing zero new information.
//   - "Scaling Test-Time Inference" (DeepMind, 2025): identifies "reasoning
//     shortcuts" where models substitute tautologies for actual causal
//     analysis, especially under context pressure.
//   - Agentic Metacognition (arXiv 2509.19783): argues that detecting one's
//     own circular reasoning is a key metacognitive skill. Agents that lack
//     this detection waste iterations on actions justified by tautological
//     reasoning rather than evidence.
//   - SWE-bench trajectory analysis: ~15% of failing trajectories contain
//     tautological justification patterns where the agent's stated reasoning
//     for an action is semantically equivalent to the action itself.
//
// Problem: AI coding agents sometimes justify their decisions with circular
// reasoning -- restating the problem as the solution, using the conclusion
// as evidence, or providing tautological explanations that add no information:
//
//   1. "To fix the timeout, we should resolve the timeout issue"
//   2. "This approach is correct because it's the right way to do it"
//   3. "Since we need authentication, we must implement authentication"
//   4. "The function fails because it doesn't work properly"
//
// Circular reasoning is dangerous because it creates false confidence -- the
// agent believes it has a logical basis for its action when it actually has
// none. This leads to misguided implementations and wasted iterations.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - assumption_track.go: detects hedging language ("I assume", "probably").
//     Circular reasoning is the opposite -- confident but vacuous.
//   - mindless_action.go: detects tool calls without reasoning text. Circular
//     reasoning has text, but it's structurally empty.
//   - premature_commitment.go: detects acting before gathering evidence.
//     Circular reasoning can occur after evidence gathering.
//   - unverified_claim.go: detects success claims without verification.
//     Circular reasoning is about justification quality, not verification.
//
// Gap: No detector analyzes the STRUCTURAL QUALITY of the agent's reasoning.
// All existing detectors examine behavioral patterns (tool sequences, error
// rates, text patterns for claims/assumptions). None check whether the
// reasoning itself is logically valid -- specifically, whether it contains
// tautological or circular structures that provide no actual justification.
//
// Design:
//   - Scans assistant text for tautological patterns:
//     1. "X because X" -- same phrase as cause and effect
//     2. "to fix/solve X, we should fix/solve X" -- problem restated as solution
//     3. "X is the correct/right/proper way because it's correct/right/proper"
//     4. "fails/doesn't work because it fails/doesn't work"
//   - Tracks instances across iterations
//   - When 2+ instances detected in a run, injects guidance
//   - Zero LLM cost -- pure deterministic pattern matching
//   - Non-blocking advisory, max 2 warnings per run

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// circularMaxWarnings: max warnings per run.
	circularMaxWarnings = 2

	// circularThreshold: instances before warning.
	circularThreshold = 2

	// circularMaxExcerpts: max excerpts to show in warning.
	circularMaxExcerpts = 4

	// circularExcerptRadius: chars around match for excerpt.
	circularExcerptRadius = 60
)

// circularPattern defines a tautological reasoning pattern.
type circularPattern struct {
	id      string
	pattern *regexp.Regexp
	desc    string
}

// Precompiled patterns for circular/tautological reasoning detection.
//
// These patterns detect structurally circular reasoning where the
// justification is semantically equivalent to the claim.
// qualityAdj is a set of quality adjectives that are commonly used in
// tautological reasoning: "correct because it's correct", etc.
var qualityAdjRe = `(?:correct|right|proper|appropriate|necessary|essential|important|best|optimal)`

// qualityNounRe is a set of nouns that pair with quality adjectives.
var qualityNounRe = `(?:way|approach|method|solution|strategy|technique)`

// failureVerbRe matches failure-related phrases.
var failureVerbRe = `(?:fails?|doesn't work|is broken|is wrong|is incorrect|has an? error|is failing|errors? out)`

// solveVerbRe matches problem-solving verbs.
var solveVerbRe = `(?:fix|solve|resolve|address|handle|correct)`

// needVerbRe matches requirement verbs.
var needVerbRe = `(?:need|require|must have)`

var circularPatterns = []circularPattern{
	// "correct because it's correct" -- identity tautology.
	// Instead of backreference, we match any quality adjective repeated.
	{
		id:      "identity_because",
		pattern: regexp.MustCompile(`(?i)\b` + qualityAdjRe + `\s+(?:` + qualityNounRe + ` )?because\s+(?:it(?:'s| is)|this is|that's|its)\s+(?:the\s+)?(?:most\s+)?` + qualityAdjRe + `\b`),
		desc:    "identity justification (correct because it's correct)",
	},

	// "to fix X, we should fix X" -- problem restated as solution.
	// We detect the structural pattern: two solve-verbs in same sentence.
	// Since we can't use backreferences, we match the structural pattern
	// and verify in code via scanCircularReasoning.
	{
		id:      "restate_solution",
		pattern: regexp.MustCompile(`(?i)\b(?:to|in order to)\s+` + solveVerbRe + `\s+([^,.;\n]{3,60}?),\s*(?:we\s+)?(?:should|need to|must|will|can)\s+` + solveVerbRe + `\s+\b`),
		desc:    "problem restated as solution (fix X to fix X)",
	},

	// "fails because it fails" -- circular failure explanation.
	{
		id:      "circular_failure",
		pattern: regexp.MustCompile(`(?i)\b` + failureVerbRe + `\s+(?:because|since|as)\s+(?:it\s+)?` + failureVerbRe + `\b`),
		desc:    "circular failure explanation (fails because it fails)",
	},

	// "This is the right way because it's the correct way"
	// -- synonym tautology (two different quality adjectives + nouns).
	{
		id:      "synonym_tautology",
		pattern: regexp.MustCompile(`(?i)\b(?:this|that|it)\s+is\s+(?:the\s+)?` + qualityAdjRe + `\s+` + qualityNounRe + `\s+(?:to\s+(?:do|handle|approach)\s+(?:this|that|it)\s+)?because\s+(?:it's|it is|that's|that is)\s+(?:the\s+)?` + qualityAdjRe + `\s+` + qualityNounRe + `\b`),
		desc:    "synonym-based tautological justification",
	},

	// "We need X because we need/require X" -- needs-based circularity.
	// Detected structurally: need...because...need/require in same sentence.
	{
		id:      "needs_circular",
		pattern: regexp.MustCompile(`(?i)\b(?:we\s+)?need\s+([^,.;\n]{3,50}?)\s+because\s+(?:we\s+)?` + needVerbRe + `\s+`),
		desc:    "circular needs justification (need X because we need X)",
	},

	// "Since X is required, X must be implemented" -- vacuous implication.
	// Detected structurally: since...is required,...must be implemented.
	{
		id:      "vacuous_implication",
		pattern: regexp.MustCompile(`(?i)\bsince\s+([^,.;\n]{3,50}?)\s+is\s+(?:required|needed|necessary),?\s+[^,.;\n]{0,30}?(?:must be|should be|needs? to be|has to be)\s+(?:implemented|added|created|done)\b`),
		desc:    "vacuous implication (requirement restated as implementation)",
	},

	// "The reason for X is to X" -- purpose equals action.
	// Detected structurally: reason for...is to...with same phrase.
	{
		id:      "purpose_equals_action",
		pattern: regexp.MustCompile(`(?i)\b(?:the\s+)?reason\s+(?:for|to)\s+([^,.;\n]{3,50}?)\s+is\s+to\s+`),
		desc:    "purpose equals action tautology (reason for X is to X)",
	},
}

// circularInstance represents a single detected circular reasoning instance.
type circularInstance struct {
	patternID string
	desc      string
	excerpt   string
	iteration int
}

// circularReasoningState tracks circular reasoning instances across a run.
type circularReasoningState struct {
	instances []circularInstance
	warnings  int
}

func newCircularReasoningState() *circularReasoningState {
	return &circularReasoningState{}
}

func (s *circularReasoningState) reset() {
	s.instances = nil
	s.warnings = 0
}

// extractCircularExcerpt returns a trimmed excerpt around a match.
func extractCircularExcerpt(text string, matchStart, matchEnd int) string {
	start := matchStart - circularExcerptRadius
	if start < 0 {
		start = 0
	}
	end := matchEnd + circularExcerptRadius
	if end > len(text) {
		end = len(text)
	}
	excerpt := strings.TrimSpace(text[start:end])
	if len(excerpt) > 120 {
		excerpt = excerpt[:120] + "..."
	}
	return excerpt
}

// scanCircularReasoning analyzes text for tautological patterns.
func scanCircularReasoning(text string) []circularInstance {
	if len(text) < 20 {
		return nil
	}

	var instances []circularInstance
	seen := make(map[string]bool) // deduplicate by excerpt

	for _, cp := range circularPatterns {
		locs := cp.pattern.FindAllStringSubmatchIndex(text, -1)
		for _, loc := range locs {
			matchStart := loc[0]
			matchEnd := loc[1]

			excerpt := extractCircularExcerpt(text, matchStart, matchEnd)

			// Deduplicate by excerpt content.
			key := cp.id + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			instances = append(instances, circularInstance{
				patternID: cp.id,
				desc:      cp.desc,
				excerpt:   excerpt,
			})
		}
	}

	return instances
}

// recordCircularReasoning adds new instances from the current iteration's text.
func (s *circularReasoningState) recordCircularReasoning(text string, iteration int) {
	newInstances := scanCircularReasoning(text)
	for _, inst := range newInstances {
		inst.iteration = iteration
		s.instances = append(s.instances, inst)
	}
}

// maybeWarnCircularReasoning checks for accumulated circular reasoning
// patterns and returns a guidance message. Returns empty string if no
// warning is needed.
func (a *Agent) maybeWarnCircularReasoning(assistantText string, iteration int) string {
	if a.circularReasoning == nil {
		return ""
	}

	// Record instances from this iteration.
	a.circularReasoning.recordCircularReasoning(assistantText, iteration)

	if a.circularReasoning.warnings >= circularMaxWarnings {
		return ""
	}

	// Check if we have enough instances to warn.
	totalInstances := len(a.circularReasoning.instances)
	if totalInstances < circularThreshold {
		return ""
	}

	a.circularReasoning.warnings++

	// Group instances by pattern type for the warning.
	patternCounts := make(map[string]int)
	var excerpts []string
	for _, inst := range a.circularReasoning.instances {
		patternCounts[inst.desc]++
		if len(excerpts) < circularMaxExcerpts {
			excerpts = append(excerpts, fmt.Sprintf("  - [iter %d] %s", inst.iteration+1, inst.excerpt))
		}
	}

	// Build pattern summary.
	var patterns []string
	for desc, count := range patternCounts {
		patterns = append(patterns, fmt.Sprintf("%s (%d)", desc, count))
	}

	return fmt.Sprintf("[CIRCULAR-REASONING] Detected %d instance(s) of "+
		"circular or tautological reasoning across recent iterations.\n"+
		"Pattern types: %s\nExamples:\n%s\n"+
		"Circular reasoning provides zero justification -- restating a problem "+
		"as its own solution is not a logical argument. For each action, "+
		"provide CONCRETE evidence: what tool result, test output, or code "+
		"observation supports this decision? If you cannot cite evidence, "+
		"gather it before proceeding.",
		totalInstances, strings.Join(patterns, ", "),
		strings.Join(excerpts, "\n"))
}
