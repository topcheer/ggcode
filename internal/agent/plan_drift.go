package agent

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

type planDriftState struct {
	captured bool
	fired    bool
	items    []planItem

	adherence planAdherenceTrend

	// Escalation bookkeeping (#plan-repair-verdict): stallStreak counts
	// consecutive drifted checks, verdictCount caps emitted verdicts.
	stallStreak  int
	verdictCount int
}

type planItem struct {
	Text     string
	Keywords []string
}

func newPlanDriftState() *planDriftState {
	return &planDriftState{}
}

func (p *planDriftState) reset() {
	p.captured = false
	p.fired = false
	p.items = nil
	p.adherence = planAdherenceTrend{}
}

func (p *planDriftState) capturePlan(planContent string) {
	if strings.TrimSpace(planContent) == "" {
		return
	}

	p.items = p.items[:0]
	p.captured = true

	for _, line := range strings.Split(planContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		item := extractPlanItem(trimmed)
		if item.Text != "" && len(item.Keywords) > 0 {
			p.items = append(p.items, item)
		}
	}

	if len(p.items) > 30 {
		p.items = p.items[:30]
	}

	debug.Log("agent", "Plan drift: captured %d plan items", len(p.items))
}

func extractPlanItem(line string) planItem {
	var text string

	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		text = strings.TrimSpace(line[2:])
	}

	if text == "" {
		text = stripNumberedPrefix(line)
	}

	if text == "" && strings.HasPrefix(line, "#") {
		hashEnd := 0
		for hashEnd < len(line) && line[hashEnd] == '#' {
			hashEnd++
		}
		rest := strings.TrimSpace(line[hashEnd:])

		if hashEnd >= 2 && hashEnd <= 3 && rest != "" {
			text = rest
		}
	}

	if text == "" {
		return planItem{}
	}

	lower := strings.ToLower(text)
	skipPrefixes := []string{"note:", "summary:", "overview", "background", "timeline", "estimated"}
	for _, sp := range skipPrefixes {
		if strings.HasPrefix(lower, sp) {
			return planItem{}
		}
	}

	keywords := extractKeywords(text)
	if len(keywords) == 0 {
		return planItem{}
	}

	return planItem{Text: text, Keywords: keywords}
}

func stripNumberedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) {
		return ""
	}
	rest := line[i:]
	if strings.HasPrefix(rest, ". ") || strings.HasPrefix(rest, ") ") {
		return strings.TrimSpace(rest[2:])
	}
	if rest == "." || rest == ")" {
		return ""
	}
	return ""
}

func extractKeywords(text string) []string {
	var keywords []string
	seen := make(map[string]bool)

	words := strings.Fields(text)
	for _, w := range words {
		clean := strings.Trim(w, ",.;:()[]{}\"'`")
		if clean == "" {
			continue
		}

		if strings.Contains(clean, "/") && strings.Contains(clean, ".") {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
			continue
		}

		if strings.HasPrefix(clean, ".") && len(clean) > 1 && isAlpha(clean[1:]) {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
			continue
		}

		if isCodeIdentifier(clean) && len(clean) >= 4 {
			lower := strings.ToLower(clean)
			if !seen[lower] {
				keywords = append(keywords, lower)
				seen[lower] = true
			}
		}
	}

	extractQuoted(text, '"', &keywords, seen)
	extractQuoted(text, '\'', &keywords, seen)
	extractQuoted(text, '`', &keywords, seen)

	return keywords
}

func extractQuoted(text string, quote byte, keywords *[]string, seen map[string]bool) {
	for {
		idx := strings.IndexByte(text, quote)
		if idx < 0 {
			break
		}

		if quote == '\'' && isContractionApostrophe(text, idx) {
			text = text[idx+1:]
			continue
		}
		rest := text[idx+1:]
		end := strings.IndexByte(rest, quote)
		if end < 0 {
			break
		}
		term := strings.TrimSpace(rest[:end])
		if len(term) >= 3 {
			lower := strings.ToLower(term)
			if !seen[lower] {
				*keywords = append(*keywords, lower)
				seen[lower] = true
			}
		}
		text = rest[end+1:]
	}
}

func isContractionApostrophe(text string, i int) bool {
	return i > 0 && i+1 < len(text) &&
		isAlphaByte(text[i-1]) && isAlphaByte(text[i+1])
}

func isCodeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	hasUpper := false
	hasUnder := false
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if r == '_' {
			hasUnder = true
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return hasUpper || hasUnder
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

func (p *planDriftState) checkPlanDrift(runStats *RunStats, _ string) string {
	if !p.captured || p.verdictCount >= pdMaxVerdicts || len(p.items) == 0 {
		return ""
	}

	workCorpus := buildPlanWorkCorpus(runStats)

	var unaddressed []string
	for _, item := range p.items {
		if len(item.Keywords) == 0 {
			continue
		}

		matched := 0
		for _, kw := range item.Keywords {
			if strings.Contains(workCorpus, kw) {
				matched++
			}
		}

		threshold := (len(item.Keywords) + 1) / 2
		if matched < threshold {
			unaddressed = append(unaddressed, truncatePlanItem(item.Text, 80))
		}
	}

	totalItems := len(p.items)
	unaddrRatio := float64(len(unaddressed)) / float64(totalItems)
	drifted := len(unaddressed) >= 2 || unaddrRatio >= 0.5

	// Fix of the fire-once bug: the old code set p.fired before evaluating,
	// so a clean first check burned the only warning for the whole run.
	if !drifted {
		p.stallStreak = 0
		return ""
	}
	p.stallStreak++

	v := planVerdictFor(p.stallStreak)
	p.verdictCount++
	if p.verdictCount >= pdMaxVerdicts {
		p.fired = true // terminal verdict emitted; stop watching
	}
	return renderPlanVerdict(v, unaddressed, totalItems)
}

func buildPlanWorkCorpus(runStats *RunStats) string {
	var b strings.Builder
	for _, f := range runStats.FilesEdited {
		b.WriteString(" ")
		b.WriteString(strings.ToLower(f))
		if idx := strings.LastIndex(f, "/"); idx >= 0 {
			b.WriteString(" ")
			b.WriteString(strings.ToLower(f[idx+1:]))
		}
	}
	for _, cmd := range runStats.CommandsRun {
		b.WriteString(" ")
		b.WriteString(strings.ToLower(cmd))
	}
	return b.String()
}

func truncatePlanItem(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func extractPlanFromArgs(args json.RawMessage) string {
	var v struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(args, &v); err != nil {
		return ""
	}
	return v.Plan
}
