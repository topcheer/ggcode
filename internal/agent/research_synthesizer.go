package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type ResearchFinding struct {
	Tool    string
	Query   string
	URL     string
	Snippet string
}

const maxResearchFindings = 24

const researchSnippetCap = 160

var findingURLRe = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)

func trimFindingSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > researchSnippetCap {

		cut := researchSnippetCap
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}

func extractFindingQuery(toolName, argsStr string) string {
	switch toolName {
	case "web_search":
		return unquoteJSONString(argsStr, "query")
	case "web_fetch", "web_reader", "mcp__web-reader__webReader":
		return unquoteJSONString(argsStr, "url")
	}
	return ""
}

func unquoteJSONString(s, key string) string {
	idx := strings.Index(s, `"`+key+`"`)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(key)+2:]
	colon := strings.Index(rest, ":")
	if colon < 0 {
		return ""
	}
	rest = rest[colon+1:]
	start := strings.Index(rest, `"`)
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && i+1 < len(rest) {
			b.WriteByte(rest[i])
			b.WriteByte(rest[i+1])
			i++
			continue
		}
		if rest[i] == '"' {
			return b.String()
		}
		b.WriteByte(rest[i])
	}
	return ""
}

func (o *overseerState) recordFinding(toolName, argsStr, resultContent string) {
	if toolName != "web_search" && toolName != "web_fetch" && toolName != "web_reader" && toolName != "mcp__web-reader__webReader" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.researchFindings) >= maxResearchFindings {
		return
	}
	q := extractFindingQuery(toolName, argsStr)

	url := ""
	if toolName != "web_search" {
		url = q
	}
	if url == "" {
		if m := findingURLRe.FindString(resultContent); m != "" {
			url = m
		}
	}
	if url == "" {
		url = q
	}
	o.researchFindings = append(o.researchFindings, ResearchFinding{
		Tool:    toolName,
		Query:   q,
		URL:     url,
		Snippet: trimFindingSnippet(resultContent),
	})
}

func (o *overseerState) findingsSnapshot() []ResearchFinding {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ResearchFinding, len(o.researchFindings))
	copy(out, o.researchFindings)
	return out
}

func hostOf(url string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")

	s = strings.TrimPrefix(s, "www.")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func SynthesizeResearchReport(goal string, findings []ResearchFinding, planned []ResearchSubQuery) string {
	if len(findings) == 0 {
		return ""
	}

	groups := []string{}
	byGroup := map[string][]ResearchFinding{}
	for _, f := range findings {
		k := strings.Join(strings.Fields(strings.ToLower(f.Query)), " ")
		if k == "" {
			k = "unattributed"
		}
		if _, ok := byGroup[k]; !ok {
			groups = append(groups, k)
		}
		byGroup[k] = append(byGroup[k], f)
	}

	var b strings.Builder
	b.WriteString("[Research Synthesis Draft] Collected retrieval findings, pre-folded per sub-query ")
	b.WriteString("(deep-research synthesizer practice): REFINE this draft into your answer; do not re-search covered ground.\n\n")

	srcNo := 0
	var gaps []string
	for _, g := range groups {
		fmt.Fprintf(&b, "## Sub-query: %s\n", g)
		hosts := map[string]bool{}
		for _, f := range byGroup[g] {
			srcNo++
			fmt.Fprintf(&b, "- [src %d] %s (%s): %s\n", srcNo, f.URL, f.Tool, f.Snippet)
			if h := hostOf(f.URL); h != "" {
				hosts[h] = true
			}
		}
		if len(hosts) > 1 {
			fmt.Fprintf(&b, "  Note: %d distinct hosts - cross-check whether they agree before citing.\n", len(hosts))
		}
		b.WriteString("\n")
	}

	if len(planned) > 0 {
		joined := strings.ToLower(strings.Join(groups, " \n "))
		for _, p := range planned {
			if !subQueryCovered(joined, p) {
				gaps = append(gaps, p.Query)
			}
		}
	}
	if len(gaps) > 0 {
		b.WriteString("## Uncovered sub-queries (gaps)\n")
		for _, gq := range gaps {
			fmt.Fprintf(&b, "- %s\n", gq)
		}
		b.WriteString("\n")
	}
	_ = goal
	return b.String()
}

const gapProbeWords = 3

func subQueryCovered(joined string, p ResearchSubQuery) bool {
	pk := strings.Join(strings.Fields(strings.ToLower(p.Query)), " ")
	if pk == "" {
		return true
	}
	fields := strings.Fields(pk)

	prefixHit := strings.Contains(joined, firstWords(pk, gapProbeWords))
	if !prefixHit && !containsCJK(pk) && contentOverlap(joined, fields) < 2 {
		return false
	}
	kws := perspectiveKeywords(p.Perspective)
	if len(kws) == 0 {
		return true
	}
	for _, kw := range kws {
		if strings.Contains(joined, kw) {
			return true
		}
	}
	return false
}

var overlapStopWords = map[string]bool{
	"and": true, "or": true, "the": true, "a": true, "an": true, "of": true,
	"in": true, "on": true, "to": true, "for": true, "with": true, "how": true,
	"its": true, "their": true, "is": true, "are": true, "technical": true,
	"details": true, "mechanisms": true,
}

func contentOverlap(joined string, fields []string) int {
	n := 0
	for _, f := range fields {
		if overlapStopWords[f] {
			continue
		}
		if strings.Contains(joined, f) {
			n++
		}
	}
	return n
}

func containsCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func firstWords(s string, n int) string {
	ws := strings.Fields(s)
	if len(ws) > n {
		ws = ws[:n]
	}
	return strings.Join(ws, " ")
}
