package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	hedgingWarnThreshold = 2

	hedgingMaxWarnings = 2

	hedgingMaxExamples = 3
)

type hedgingPattern struct {
	level   string
	pattern *regexp.Regexp
}

var actionHedgingPatterns = []hedgingPattern{

	{"HIGH", regexp.MustCompile(`(?i)\bthis should hopefully\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bhopefully this (?:fix|work|resolv|address)`)},
	{"HIGH", regexp.MustCompile(`(?i)\bthis might (?:fix|work|resolv|be|not)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bthis may (?:fix|work|not|or)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bI'm not (?:entirely |completely |100% )?sure\b.*\b(but|yet|still)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bnot sure if (?:this|that)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bbest guess\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\beducated guess\b`)},

	{"HIGH", regexp.MustCompile(`(?i)\bif (?:this|that) (?:doesn't|does not|fails?)\b`)},

	{"HIGH", regexp.MustCompile(`(?i)\b(?:we|i)'?(?:ll| will)? ?(?:use|fall back to|switch to)? ?a? ?fallback\b[^.]{0,40}\bif\b`)},

	{"MEDIUM", regexp.MustCompile(`(?i)\blet'?s try (?:this|that|it)\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\btrial and error\b`)},
	{"HIGH", regexp.MustCompile(`(?i)\bthis is (?:a )?shot in the dark\b`)},

	{"MEDIUM", regexp.MustCompile(`(?i)\bI think this (?:should|will|might|could|may)\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bthis ought to\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bthis is worth a try\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bsee if (?:this|that) works\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bfingers crossed\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bno guarantees?\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bcould be wrong\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bmight be (?:wrong|mistaken|incorrect)\b`)},
	{"MEDIUM", regexp.MustCompile(`(?i)\bI could be off\b`)},
}

type hedgingHit struct {
	level   string
	excerpt string
}

type actionHedgingState struct {
	warnings int
}

func newActionHedgingState() *actionHedgingState {
	return &actionHedgingState{}
}

func (s *actionHedgingState) reset() {
	s.warnings = 0
}

func isMutationTool(toolName string) bool {
	return sourceMutatingTools[toolName] ||
		toolName == "git_commit" || toolName == "git_add" || toolName == "git_reset" ||
		toolName == "git_revert" || toolName == "git_checkout" || toolName == "git_stash"
}

func scanActionHedging(text string) []hedgingHit {
	if len(text) == 0 {
		return nil
	}

	var hits []hedgingHit
	seen := make(map[string]bool)

	for _, hp := range actionHedgingPatterns {
		locs := hp.pattern.FindAllStringIndex(text, -1)
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

			key := hp.level + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, hedgingHit{
				level:   hp.level,
				excerpt: excerpt,
			})
		}
	}

	return hits
}

func (a *Agent) maybeWarnActionHedging(text string, hasMutation bool) string {
	if a.actionHedging == nil {
		return ""
	}
	if a.actionHedging.warnings >= hedgingMaxWarnings {
		return ""
	}

	if !hasMutation {
		return ""
	}

	hits := scanActionHedging(text)
	if len(hits) < hedgingWarnThreshold {
		return ""
	}

	highCount := 0
	for _, h := range hits {
		if h.level == "HIGH" {
			highCount++
		}
	}

	a.actionHedging.warnings++

	var examples []string
	for _, h := range hits {
		if len(examples) >= hedgingMaxExamples {
			break
		}
		examples = append(examples, fmt.Sprintf("  [%s] ...%s...", h.level, h.excerpt))
	}

	severity := "INFO"
	if highCount >= 1 {
		severity = "WARNING"
	}

	return fmt.Sprintf(
		"[%s-action-hedging] Detected %d verbalized uncertainty signal(s) "+
			"(%d HIGH, %d MEDIUM) in a turn that includes a code mutation. "+
			"Your hedging language indicates you are not confident in the fix "+
			"you are applying. This is a metacognitive signal: you have enough "+
			"awareness to express doubt but are proceeding without verification. "+
			"Before making further edits: (1) re-read the relevant code to confirm "+
			"root cause, (2) check if there is existing test coverage, "+
			"(3) consider asking the user for clarification on ambiguous requirements. "+
			"Do not apply speculative edits when you can verify first.\n"+
			"Detected hedging signals:\n%s",
		severity, len(hits), highCount, len(hits)-highCount,
		strings.Join(examples, "\n"),
	)
}
