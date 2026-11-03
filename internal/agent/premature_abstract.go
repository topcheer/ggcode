package agent

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	prematureAbstrWarnThreshold = 2

	prematureAbstrMaxWarnings = 2

	prematureAbstrMaxExcerpts = 3
)

type prematureAbstrPattern struct {
	category string
	pattern  *regexp.Regexp
}

var prematureAbstrPatterns = []prematureAbstrPattern{

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

type prematureAbstrHit struct {
	category string
	excerpt  string
}

type prematureAbstrState struct {
	warnings int
}

func newPrematureAbstrState() *prematureAbstrState {
	return &prematureAbstrState{}
}

func (s *prematureAbstrState) reset() {
	s.warnings = 0
}

func scanPrematureAbstraction(text string) []prematureAbstrHit {
	if len(text) == 0 {
		return nil
	}

	var hits []prematureAbstrHit
	seen := make(map[string]bool)

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
