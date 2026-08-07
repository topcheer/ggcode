package agent

// Premature Abstraction Detector
//
// Research basis:
//   - LinkedIn/Umut Eser (2025): "All AI coding agents I have tested have
//     common issues - they all try to build full-fledged code which has a ton
//     of unnecessary components and interactions."
//   - StackOverflow Blog (2026): "Coding agents are giving everyone decision
//     fatigue" - agents create excessive abstraction layers, factory patterns,
//     and configuration frameworks where simple code would suffice.
//   - Code Smell 273 - Overengineering (Medium/javarevisited, 2025): AI can
//     detect overengineered code by analyzing its structure and suggesting
//     refactorings to simplify excessive abstractions.
//   - Pragmatic Engineer (2025): "More code generated will lead to more
//     problems... weak software engineering practices start to hurt sooner."
//   - The AI Overengineering Trap (Trace3, 2025): Overengineering is the #1
//     failure mode of AI-generated code - agents create factory-of-factory
//     patterns, interface hierarchies with single implementations, config
//     systems for values used once, and generic frameworks for specific needs.
//
// Problem: AI coding agents routinely over-engineer solutions within the scope
// of the requested task. Unlike scope creep (which expands to unsolicited
// areas), premature abstraction inflates complexity within the requested area:
//
//  1. "I created a Factory pattern to handle different request types" - when
//     only one request type exists
//  2. "I built a configurable plugin system for extensibility" - when the user
//     asked for a simple function
//  3. "I defined an interface so we can swap implementations later" - when
//     there is exactly one implementation and no requirement to swap
//  4. "I created a generic handler framework" - when a direct function call
//     would work
//
// This wastes tokens, inflates the diff, and creates maintenance burden. The
// code works but is unnecessarily complex - a "works but wrong" failure.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - scope_creep_detect.go: catches expanding BEYOND the task scope. This
//     detector catches over-engineering WITHIN the task scope.
//   - premature_commitment.go: checks if evidence was gathered before first
//     edit, not whether the solution itself is over-engineered.
//   - interface_design.go: checks interface naming/conventions, not whether
//     the interface itself is prematurely introduced.
//
// Gap: No detector scans assistant text for language indicating the agent is
// introducing design patterns, abstraction layers, or generic frameworks that
// may be premature for the actual requirement.
//
// Design:
//   - Scans assistant text for over-engineering / premature abstraction language
//   - Three categories: pattern_inflation (factory/builder/strategy etc.),
//     abstraction_layers (interface/hierarchy/generic where unnecessary),
//     config_systems (configurable/extensible/pluggable for simple needs)
//   - Threshold: 2+ signals → inject guidance to simplify
//   - Zero LLM cost - pure deterministic text pattern matching
//   - Fires at most 2 times per run (advisory, non-blocking)

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// prematureAbstrWarnThreshold: at this count of signals, inject guidance.
	prematureAbstrWarnThreshold = 2

	// prematureAbstrMaxWarnings: max warnings per run.
	prematureAbstrMaxWarnings = 2

	// prematureAbstrMaxExcerpts: max signal excerpts to include in hint.
	prematureAbstrMaxExcerpts = 3
)

// prematureAbstrPattern represents a detected over-engineering signal.
type prematureAbstrPattern struct {
	category string // "pattern_inflation", "abstraction_layers", or "config_systems"
	pattern  *regexp.Regexp
}

// Precompiled patterns for premature abstraction detection.
// Case-insensitive. Patterns target agent's own description of what it built.
var prematureAbstrPatterns = []prematureAbstrPattern{
	// Pattern inflation: introducing GoF design patterns where simple code suffices
	{
		"pattern_inflation",
		regexp.MustCompile(`(?i)(?:i\s+(?:have\s+)?(?:created?|implemented?|built|added|introduced)\s+(?:a\s+)?(?:factory|abstract\s+factory|builder|strategy|command|visitor|chain\s+of\s+responsibility)\s+(?:pattern|class|interface))`),
	},
	{
		"pattern_inflation",
		regexp.MustCompile(`(?i)(?:let\s+me\s+(?:create|implement)\s+(?:a\s+)?(?:factory|builder|strategy|provider|registry)\s+(?:pattern|class|to))`),
	},
	{
		"pattern_inflation",
		regexp.MustCompile(`(?i)(?:using\s+(?:the\s+)?(?:factory|builder|strategy|visitor|command)\s+pattern\s+(?:to|for|so\s+that))`),
	},

	// Abstraction layers: interface/type hierarchy with single implementation
	{
		"abstraction_layers",
		regexp.MustCompile(`(?i)(?:i(?:'ve|\s+have)?\s+(?:defined|created|introduced)\s+(?:an?\s+)?(?:interface|abstract\s+(?:class|type)|base\s+(?:class|type))\s+(?:so\s+(?:that|we)|to\s+(?:allow|enable|support|facilitate)))`),
	},
	{
		"abstraction_layers",
		regexp.MustCompile(`(?i)(?:for\s+(?:future\s+)?(?:extensibility|flexibility|maintainability),?\s+i\s+(?:have\s+)?(?:created|defined|added|abstracted))`),
	},
	{
		"abstraction_layers",
		regexp.MustCompile(`(?i)(?:to\s+(?:allow|support|enable)\s+(?:swapping|swappable|interchangeable|multiple)\s+(?:implementations?|backends?|providers?|strategies?))`),
	},
	{
		"abstraction_layers",
		regexp.MustCompile(`(?i)(?:i(?:'ve|\s+have)?\s+(?:created|built)\s+(?:a\s+)?generic\s+(?:handler|processor|manager|resolver|wrapper|framework))`),
	},

	// Config systems: configurable/pluggable systems for values used once
	{
		"config_systems",
		regexp.MustCompile(`(?i)(?:i\s+(?:have\s+)?(?:created|added|built)\s+(?:a\s+)?(?:config(?:urable|uration)?|pluggable|extensible)\s+(?:system|framework|mechanism|layer))`),
	},
	{
		"config_systems",
		regexp.MustCompile(`(?i)(?:made\s+(?:this|it)\s+(?:fully\s+)?configurable\s+(?:so|to|for))`),
	},
	{
		"config_systems",
		regexp.MustCompile(`(?i)(?:i\s+(?:also\s+)?(?:added|created)\s+(?:a\s+)?(?:plugin|hook|extension)\s+(?:system|point|mechanism)\s+(?:so|to|for))`),
	},
}

// prematureAbstrHit records a single detected over-engineering signal.
type prematureAbstrHit struct {
	category string
	excerpt  string
}

// prematureAbstrState tracks over-engineering detections across a run.
type prematureAbstrState struct {
	warnings int
}

func newPrematureAbstrState() *prematureAbstrState {
	return &prematureAbstrState{}
}

func (s *prematureAbstrState) reset() {
	s.warnings = 0
}

// scanPrematureAbstraction analyzes assistant text for over-engineering signals.
func scanPrematureAbstraction(text string) []prematureAbstrHit {
	if len(text) == 0 {
		return nil
	}

	var hits []prematureAbstrHit
	seen := make(map[string]bool) // deduplicate by excerpt

	for _, ap := range prematureAbstrPatterns {
		locs := ap.pattern.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			start := loc[0]
			excerptStart := start - 20
			if excerptStart < 0 {
				excerptStart = 0
			}
			excerptEnd := loc[1] + 40
			if excerptEnd > len(text) {
				excerptEnd = len(text)
			}
			excerpt := strings.TrimSpace(text[excerptStart:excerptEnd])
			if len(excerpt) > 80 {
				excerpt = excerpt[:80] + "..."
			}

			key := ap.category + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, prematureAbstrHit{
				category: ap.category,
				excerpt:  excerpt,
			})
		}
	}

	return hits
}

// maybeWarnPrematureAbstraction checks assistant text for over-engineering
// language and returns a guidance message if the threshold is exceeded.
// Returns empty string if no warning is needed.
func (a *Agent) maybeWarnPrematureAbstraction(text string) string {
	if a.prematureAbstr == nil {
		return ""
	}
	if a.prematureAbstr.warnings >= prematureAbstrMaxWarnings {
		return ""
	}

	hits := scanPrematureAbstraction(text)
	if len(hits) < prematureAbstrWarnThreshold {
		return ""
	}

	// Count by category
	cats := make(map[string]int)
	for _, h := range hits {
		cats[h.category]++
	}

	a.prematureAbstr.warnings++

	var excerpts []string
	for _, h := range hits {
		if len(excerpts) >= prematureAbstrMaxExcerpts {
			break
		}
		excerpts = append(excerpts, fmt.Sprintf("  [%s] ...%s...", h.category, h.excerpt))
	}

	header := fmt.Sprintf(
		"[over-engineering] Detected %d signal(s) of premature abstraction "+
			"(%d pattern inflation, %d unnecessary abstraction layers, %d config/plugin systems). "+
			"AI agents routinely over-engineer by introducing factory patterns, interface hierarchies, "+
			"and configurable frameworks where simple code would suffice. "+
			"This inflates the diff, wastes tokens, and creates maintenance burden. "+
			"Prefer direct functions over factories, concrete types over interfaces with single implementations, "+
			"and hardcoded values over config systems unless flexibility is explicitly needed. "+
			"YAGNI (You Aren't Gonna Need It): build for the current requirement, not hypothetical future needs.\n",
		len(hits), cats["pattern_inflation"], cats["abstraction_layers"], cats["config_systems"],
	)
	return header + "Detected signals:\n" + strings.Join(excerpts, "\n")
}
