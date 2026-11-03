package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	scopeCreepWarnThreshold = 2

	scopeCreepMaxWarnings = 2

	scopeCreepMaxExcerpts = 3
)

type scopeCreepPattern struct {
	category string
	pattern  *regexp.Regexp
}

var scopeCreepPatterns = []scopeCreepPattern{

	{"action", regexp.MustCompile(`(?i)(?:while\s+i(?:'m|\s+am)\s+at\s+it|since\s+i(?:'m|\s+am)\s+(?:already\s+)?here|while\s+i(?:'m|\s+am)\s+in\s+(?:this|the)\s+(?:file|area|section))`)},

	{"action", regexp.MustCompile(`(?i)(?:i(?:'ll|\s+will)\s+also\s+(?:fix|update|clean|refactor|modify|change|add|remove|implement|handle)|let\s+me\s+also\s+(?:fix|update|clean|refactor|modify|change|add|remove)|i\s+should\s+also\s+(?:fix|update|clean|refactor|modify|change|add|remove)|i\s+also\s+(?:went|fixed|updated|cleaned|refactored))`)},
	{"action", regexp.MustCompile(`(?i)(?:i(?:'ve|\s+have)\s+(?:also\s+)?(?:gone\s+ahead|taken\s+the\s+liberty|decided)\s+(?:and\s+)?(?:to\s+)?(?:fix|update|clean|refactor|modify|change|add|remove))`)},
	{"action", regexp.MustCompile(`(?i)(?:i\s+(?:also\s+)?(?:cleaned\s+up|tidied\s+up|fixed\s+up)\s+(?:the\s+)?(?:imports?|formatting|whitespace|naming|comments?))`)},

	{"justification", regexp.MustCompile(`(?i)(?:to\s+(?:fully|properly|better|completely|thoroughly)\s+(?:fix|handle|address|solve)\s+(?:this|that),?\s+i\s+(?:also\s+)?(?:need\s+to|had\s+to|must)\s+(?:fix|update|change|modify|refactor))`)},

	{"justification", regexp.MustCompile(`(?i)(?:it\s+(?:would\s+)?(?:also\s+)?be\s+(?:good|helpful|useful|better|wise)\s+to\s+(?:also\s+)?(?:fix|update|clean|refactor))`)},
	{"justification", regexp.MustCompile(`(?i)(?:i\s+(?:noticed|found|spotted|saw)\s+(?:some|a\s+few|several)\s+(?:other|additional)\s+(?:issues?|problems?|things)\s+(?:that\s+)?(?:i\s+)?(?:also\s+)?(?:fixed|should|could))`)},
	{"justification", regexp.MustCompile(`(?i)(?:expanding\s+(?:my|the)\s+(?:scope|changes?|search)\s+to\s+(?:include|cover|also))`)},
	{"justification", regexp.MustCompile(`(?i)(?:i\s+went\s+(?:ahead\s+)?beyond\s+(?:the\s+)?(?:original|assigned|initial)\s+(?:scope|task|request))`)},
}

type scopeCreepHit struct {
	category string
	excerpt  string
}

type scopeCreepState struct {
	warnings int
}

func newScopeCreepState() *scopeCreepState {
	return &scopeCreepState{}
}

func (s *scopeCreepState) reset() {
	s.warnings = 0
}

func scanScopeCreep(text string) []scopeCreepHit {
	if len(text) == 0 {
		return nil
	}

	var hits []scopeCreepHit
	seen := make(map[string]bool)

	for _, sp := range scopeCreepPatterns {
		locs := sp.pattern.FindAllStringIndex(text, -1)
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

			key := sp.category + ":" + excerpt
			if seen[key] {
				continue
			}
			seen[key] = true

			hits = append(hits, scopeCreepHit{
				category: sp.category,
				excerpt:  excerpt,
			})
		}
	}

	return hits
}

func (a *Agent) maybeWarnScopeCreep(text string) string {
	if a.scopeCreep == nil {
		return ""
	}
	if a.scopeCreep.warnings >= scopeCreepMaxWarnings {
		return ""
	}

	hits := scanScopeCreep(text)
	if len(hits) < scopeCreepWarnThreshold {
		return ""
	}

	actionCount := 0
	for _, h := range hits {
		if h.category == "action" {
			actionCount++
		}
	}

	a.scopeCreep.warnings++

	var excerpts []string
	for _, h := range hits {
		if len(excerpts) >= scopeCreepMaxExcerpts {
			break
		}
		excerpts = append(excerpts, fmt.Sprintf("  [%s] ...%s...", h.category, h.excerpt))
	}

	header := fmt.Sprintf(
		"[scope-creep] Detected %d signal(s) of unsolicited scope expansion "+
			"(%d unsolicited actions, %d justifications for expanding scope). "+
			"Making changes beyond the user's request increases diff size, review burden, "+
			"and regression risk in code the user didn't ask to modify. "+
			"Stick to the requested task. If you spot a genuinely related issue, note it "+
			"for the user rather than fixing it unprompted. "+
			"Avoid 'while I'm at it' expansions.\n",
		len(hits), actionCount, len(hits)-actionCount,
	)
	return header + "Detected signals:\n" + strings.Join(excerpts, "\n")
}
