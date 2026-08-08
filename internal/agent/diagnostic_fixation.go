package agent

// Diagnostic Fixation Detector - Stale Hypothesis Persistence
//
// Research basis:
//   - Belief Perseverance (Anderson et al., cognitive psychology): humans
//     and LLMs cling to initial beliefs even after disconfirming evidence.
//     In agentic loops, an early diagnostic hypothesis ("the bug is in
//     auth.go") gets repeated turn after turn without evolution.
//   - Anchoring Effect in LLMs (arXiv:2505.15392): LLMs anchor heavily on
//     their first hypothesis and are slow to abandon it.
//   - Reflexion (Shinn et al., 2023): self-reflection only helps when the
//     agent actually UPDATES its mental model. Repeating the same diagnosis
//     without updating = failed reflection = wasted iterations.
//   - AgentDebug / "Where LLM Agents Fail" (arXiv:2509.25370): agents that
//     don't re-diagnose after failures keep looping on the same root cause,
//     achieving up to 26% improvement when forced to re-diagnose.
//
// Problem: An AI coding agent forms a causal hypothesis early ("the problem
// is the database connection pool", "the bug is in auth.go"). Each turn it
// restates the same diagnosis in its reasoning, even as tool evidence
// accumulates. The agent is stuck in a confirmation loop -- it keeps looking
// at the same entity without broadening its search.
//
// This is distinct from solution_fixation.go (which tracks FAILED EDITS to
// the same file). This detector tracks DIAGNOSTIC LANGUAGE in text -- the
// agent's stated root-cause claims -- about ANY entity (files, functions,
// modules, services), regardless of whether edits happen or succeed.
//
// Example failure mode:
//   Turn 1: "The problem is in parseConfig() -- it's not handling nil"
//   Turn 2: [reads file] "The root cause is in parseConfig()"
//   Turn 3: [edits elsewhere] "The issue is still parseConfig()"
//   Turn 4: "parseConfig() is the culprit"
//   → Agent never broadens its diagnosis despite 4 turns of no resolution.
//
// Detection approach:
//   - Scan assistant text for causal/diagnostic claim phrases
//   - Extract the entity (file path or code identifier) from each claim
//   - Track how many distinct turns each entity appears as a claim subject
//   - If the same entity is the subject of diagnostic claims across 3+
//     turns, warn the agent to re-diagnose
//   - Zero LLM cost -- pure deterministic text pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	// diagnosticFixationTurnThreshold: entity must be claimed across this
	// many distinct turns before warning.
	diagnosticFixationTurnThreshold = 3

	// diagnosticFixationMaxWarnings: max warnings per run.
	diagnosticFixationMaxWarnings = 2

	// diagnosticFixationMaxExamples: max stale entities to show in hint.
	diagnosticFixationMaxExamples = 3
)

// diagnosticClaimRe matches causal/diagnostic assertion phrases. After the
// match, we extract the entity from the text that follows.
var diagnosticClaimRe = regexp.MustCompile(
	`(?i)` +
		`(?:the\s+(?:problem|issue|bug|error|root\s+cause|culprit)\s+` +
		`(?:is|was|seems?\s+to\s+be|appears?\s+to\s+be|lies?\s+in|is\s+in)\b` +
		`|caused\s+by\b|is\s+causing\b|` +
		`this\s+(?:is|happens|occurs)\s+because\b|` +
		`the\s+fix\s+(?:is|needs?\s+to\s+be)\s+in\b)`,
)

// diagFilePathRe matches file paths in the post-claim region (e.g., auth.go,
// config.yaml, internal/agent/loop.go).
var diagFilePathRe = regexp.MustCompile(`[\w./-]+\.[A-Za-z]{1,5}\b`)

// diagCodeIdentRe matches identifiers that look like code symbols (not plain
// English words): camelCase, snake_case, ALL_CAPS acronyms, or
// acronym-prefixed PascalCase (e.g., HTTPClient).
// This filters false positives like "server", "crashes", "problem".
var diagCodeIdentRe = regexp.MustCompile(
	`\b(?:[a-z]+[A-Z]\w*|[a-z][a-z0-9]*_[a-z0-9_]+|[A-Z]{2,}[A-Z0-9_]*[a-z]\w*|[A-Z]{2,}[A-Z0-9_]*)\b`,
)

// diagnosticFixationState tracks diagnostic claims across a run.
type diagnosticFixationState struct {
	warnings int
	// entityTurns maps entity → number of distinct turns it appeared as a
	// claim subject.
	entityTurns map[string]int
	// turnSeen tracks entities already counted for the current turn (to
	// avoid double-counting within one assistant response).
	turnSeen map[string]bool
	// warned tracks entities already included in a warning, to avoid
	// re-warning about the same entity on subsequent turns.
	warned map[string]bool
}

func newDiagnosticFixationState() *diagnosticFixationState {
	return &diagnosticFixationState{
		entityTurns: make(map[string]int),
		turnSeen:    make(map[string]bool),
		warned:      make(map[string]bool),
	}
}

func (s *diagnosticFixationState) reset() {
	s.warnings = 0
	s.entityTurns = make(map[string]int)
	s.turnSeen = make(map[string]bool)
	s.warned = make(map[string]bool)
}

// extractClaimEntity finds the primary entity (file path or code identifier)
// in the text immediately following a diagnostic claim phrase.
func extractClaimEntity(text string, matchEnd int) string {
	regionEnd := matchEnd + 80
	if regionEnd > len(text) {
		regionEnd = len(text)
	}
	region := text[matchEnd:regionEnd]

	// Cut at sentence-ending punctuation to avoid grabbing from next sentence.
	if idx := strings.IndexAny(region, "\n;!"); idx >= 0 {
		region = region[:idx]
	}
	// Don't cut at "." for file paths -- handle separately.
	// Trim leading whitespace/articles.
	region = strings.TrimLeft(region, " \t:'\"")
	// Strip common leading articles.
	for _, art := range []string{"the ", "a ", "an ", "in ", "with ", "that "} {
		region = strings.TrimPrefix(region, art)
	}
	region = strings.TrimLeft(region, " \t")

	// Priority 1: file paths.
	if fp := diagFilePathRe.FindString(region); fp != "" {
		return fp
	}

	// Priority 2: code-like identifiers.
	if ident := diagCodeIdentRe.FindString(region); ident != "" {
		return ident
	}

	return ""
}

// recordClaims extracts diagnostic claims from text and records unique
// entities for the current turn.
func (s *diagnosticFixationState) recordClaims(text string) []string {
	s.turnSeen = make(map[string]bool)

	if len(text) == 0 {
		return nil
	}

	var claimed []string
	matches := diagnosticClaimRe.FindAllStringIndex(text, -1)
	for _, loc := range matches {
		entity := extractClaimEntity(text, loc[1])
		if entity == "" {
			continue
		}
		if !s.turnSeen[entity] {
			s.turnSeen[entity] = true
			s.entityTurns[entity]++
			claimed = append(claimed, entity)
		}
	}

	return claimed
}

// maybeWarnDiagnosticFixation checks assistant text for repeated stale
// diagnostic claims and returns a guidance message if the threshold is
// exceeded. Returns empty string if no warning is needed.
func (a *Agent) maybeWarnDiagnosticFixation(text string) string {
	if a.diagnosticFixation == nil {
		return ""
	}
	if a.diagnosticFixation.warnings >= diagnosticFixationMaxWarnings {
		// Still record claims for tracking, but don't warn again.
		a.diagnosticFixation.recordClaims(text)
		return ""
	}

	a.diagnosticFixation.recordClaims(text)

	// Find entities claimed across the threshold number of distinct turns
	// that have NOT been previously warned about.
	var stale []string
	for entity, turns := range a.diagnosticFixation.entityTurns {
		if turns >= diagnosticFixationTurnThreshold && !a.diagnosticFixation.warned[entity] {
			stale = append(stale, entity)
		}
	}

	if len(stale) == 0 {
		return ""
	}

	a.diagnosticFixation.warnings++

	// Sort by turn count descending (most stale first).
	sort.Slice(stale, func(i, j int) bool {
		return a.diagnosticFixation.entityTurns[stale[i]] >
			a.diagnosticFixation.entityTurns[stale[j]]
	})

	if len(stale) > diagnosticFixationMaxExamples {
		stale = stale[:diagnosticFixationMaxExamples]
	}

	// Mark warned entities so they don't trigger again.
	for _, e := range stale {
		a.diagnosticFixation.warned[e] = true
	}

	var parts []string
	for _, e := range stale {
		parts = append(parts, fmt.Sprintf("  - %s (claimed across %d turns)",
			e, a.diagnosticFixation.entityTurns[e]))
	}

	return fmt.Sprintf(
		"[WARNING-diagnostic-fixation] You have restated the same diagnostic "+
			"claim across %d+ turns without evolving your hypothesis:\n%s\n"+
			"This is a belief-perseverance pattern: repeating a root-cause "+
			"diagnosis without updating it wastes iterations. If the issue "+
			"were actually here, it would likely be resolved by now.\n"+
			"Step back and RE-DIAGNOSE: consider whether the root cause is "+
			"elsewhere (a different file, an upstream caller, a configuration "+
			"issue, or an environmental factor). Broaden your search rather "+
			"than deepening the same hypothesis.\n",
		diagnosticFixationTurnThreshold,
		strings.Join(parts, "\n"),
	)
}
