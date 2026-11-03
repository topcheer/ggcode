package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	circularMaxWarnings = 2

	circularThreshold = 2

	circularMaxExcerpts = 4

	circularExcerptRadius = 60
)

type circularPattern struct {
	id      string
	pattern *regexp.Regexp
	desc    string
}

var qualityAdjRe = `(?:correct|right|proper|appropriate|necessary|essential|important|best|optimal)`

var qualityNounRe = `(?:way|approach|method|solution|strategy|technique)`

var failureVerbRe = `(?:fails?|doesn't work|is broken|is wrong|is incorrect|has an? error|is failing|errors? out)`

var solveVerbRe = `(?:fix|solve|resolve|address|handle|correct)`

var needVerbRe = `(?:need|require|must have)`

var circularPatterns = []circularPattern{

	{
		id:      "identity_because",
		pattern: regexp.MustCompile(`(?i)\b` + qualityAdjRe + `\s+(?:` + qualityNounRe + ` )?because\s+(?:it(?:'s| is)|this is|that's|its)\s+(?:the\s+)?(?:most\s+)?` + qualityAdjRe + `\b`),
		desc:    "identity justification (correct because it's correct)",
	},

	{
		id:      "restate_solution",
		pattern: regexp.MustCompile(`(?i)\b(?:to|in order to)\s+` + solveVerbRe + `\s+([^,.;\n]{3,60}?),\s*(?:we\s+)?(?:should|need to|must|will|can)\s+` + solveVerbRe + `\s+([^,.;\n]{3,60}?)(?:[.,;\n]|$)`),
		desc:    "problem restated as solution (fix X to fix X)",
	},

	{
		id:      "circular_failure",
		pattern: regexp.MustCompile(`(?i)\b` + failureVerbRe + `\s+(?:because|since|as)\s+(?:it\s+)?` + failureVerbRe + `\b`),
		desc:    "circular failure explanation (fails because it fails)",
	},

	{
		id:      "synonym_tautology",
		pattern: regexp.MustCompile(`(?i)\b(?:this|that|it)\s+is\s+(?:the\s+)?` + qualityAdjRe + `\s+` + qualityNounRe + `\s+(?:to\s+(?:do|handle|approach)\s+(?:this|that|it)\s+)?because\s+(?:it's|it is|that's|that is)\s+(?:the\s+)?` + qualityAdjRe + `\s+` + qualityNounRe + `\b`),
		desc:    "synonym-based tautological justification",
	},

	{
		id:      "needs_circular",
		pattern: regexp.MustCompile(`(?i)\b(?:we\s+)?need\s+([^,.;\n]{3,50}?)\s+because\s+(?:we\s+)?` + needVerbRe + `\s+([^,.;\n]{3,60}?)(?:[.,;\n]|$)`),
		desc:    "circular needs justification (need X because we need X)",
	},

	{
		id:      "vacuous_implication",
		pattern: regexp.MustCompile(`(?i)\bsince\s+([^,.;\n]{3,50}?)\s+is\s+(?:required|needed|necessary),?\s+([^,.;\n]{0,30}?)\s*(?:must be|should be|needs? to be|has to be)\s+(?:implemented|added|created|done)\b`),
		desc:    "vacuous implication (requirement restated as implementation)",
	},

	{
		id:      "purpose_equals_action",
		pattern: regexp.MustCompile(`(?i)\b(?:the\s+)?reason\s+(?:for|to)\s+([^,.;\n]{3,50}?)\s+is\s+to\s+([^,.;\n]{3,60}?)(?:[.,;\n]|$)`),
		desc:    "purpose equals action tautology (reason for X is to X)",
	},
}

type circularInstance struct {
	patternID string
	desc      string
	excerpt   string
	iteration int
}

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

var identityCheckPatterns = map[string]bool{
	"restate_solution":      true,
	"needs_circular":        true,
	"vacuous_implication":   true,
	"purpose_equals_action": true,
}

func normalizePhrase(s string) []string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(",", "", ".", "", ";", "", ":", "", "!", "", "?", "", "\"", "", "'", "").Replace(s)
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "a" || f == "an" || f == "the" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func samePhrase(a, b string) bool {
	ta, tb := normalizePhrase(a), normalizePhrase(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if strings.Join(ta, " ") == strings.Join(tb, " ") {
		return true
	}
	setA := make(map[string]bool, len(ta))
	for _, t := range ta {
		setA[t] = true
	}
	inter := 0
	for _, t := range tb {
		if setA[t] {
			inter++
		}
	}
	minLen := len(ta)
	if len(tb) < minLen {
		minLen = len(tb)
	}
	return float64(inter)/float64(minLen) >= 0.8
}

func scanCircularReasoning(text string) []circularInstance {
	if len(text) < 20 {
		return nil
	}

	var instances []circularInstance
	seen := make(map[string]bool)

	for _, cp := range circularPatterns {
		locs := cp.pattern.FindAllStringSubmatchIndex(text, -1)
		for _, loc := range locs {
			matchStart := loc[0]
			matchEnd := loc[1]

			if identityCheckPatterns[cp.id] {
				if len(loc) < 6 {
					continue
				}
				g1 := text[loc[2]:loc[3]]
				g2 := text[loc[4]:loc[5]]
				if !samePhrase(g1, g2) {
					continue
				}
			}

			excerpt := extractCircularExcerpt(text, matchStart, matchEnd)

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

func (s *circularReasoningState) recordCircularReasoning(text string, iteration int) {
	newInstances := scanCircularReasoning(text)
	for _, inst := range newInstances {
		inst.iteration = iteration
		s.instances = append(s.instances, inst)
	}
}

func (a *Agent) maybeWarnCircularReasoning(assistantText string, iteration int) string {
	if a.circularReasoning == nil {
		return ""
	}

	a.circularReasoning.recordCircularReasoning(assistantText, iteration)

	if a.circularReasoning.warnings >= circularMaxWarnings {
		return ""
	}

	totalInstances := len(a.circularReasoning.instances)
	if totalInstances < circularThreshold {
		return ""
	}

	a.circularReasoning.warnings++

	patternCounts := make(map[string]int)
	var excerpts []string
	for _, inst := range a.circularReasoning.instances {
		patternCounts[inst.desc]++
		if len(excerpts) < circularMaxExcerpts {
			excerpts = append(excerpts, fmt.Sprintf("  - [iter %d] %s", inst.iteration+1, inst.excerpt))
		}
	}

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
