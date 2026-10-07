package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// Research synthesizer (r343 deep-research third component; sa-130). The
// r365 research-report gate (research_report_gate.go) DEMANDS a structured
// synthesis from the model but hands it nothing but a call count - the
// model must re-read its own scattered search results from context.
// Production deep-research systems (Perplexity Sonar Deep Research,
// Open Deep Research; DRA roadmap arXiv:2506.18096) close this loop with a
// programmatic SYNTHESIZER: retrieval findings are collected as data and
// folded into a structured draft the model refines, not reconstructs.
//
// This file provides both halves, zero-LLM-cost and deterministic:
//   - overseer.recordFinding: collects (tool, query, first URL, snippet)
//     from every successful web_search / web_fetch result, post-redaction
//   - SynthesizeResearchReport: groups findings by sub-query, numbers
//     sources, flags divergent domains per group (conflict candidates),
//     and lists uncovered sub-queries (gaps) - the same four-section
//     structure the r365 gate asks for, now pre-assembled.

// ResearchFinding is one collected retrieval datum.
type ResearchFinding struct {
	Tool    string // web_search or web_fetch
	Query   string // search query (web_search) or fetched URL (web_fetch)
	URL     string // first result/source URL if extractable, else Query
	Snippet string // short excerpt for the draft (already redacted)
}

// maxResearchFindings bounds collection: a multi-hop run rarely needs more
// than 24 findings for a synthesis draft, and the cap keeps memory O(1).
const maxResearchFindings = 24

// researchSnippetCap bounds each snippet so 24 findings stay readable.
const researchSnippetCap = 160

// findingURLRe extracts the first absolute http(s) URL from result text.
var findingURLRe = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)

// trimFindingSnippet collapses whitespace and caps the length.
func trimFindingSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > researchSnippetCap {
		s = s[:researchSnippetCap]
	}
	return s
}

// extractFindingQuery pulls the search query (web_search) or URL argument
// (web_fetch) out of raw tool-call arguments JSON. Best-effort: returns ""
// when the key is absent or the JSON is malformed - the finding still
// records with an empty query and groups under "unattributed".
func extractFindingQuery(toolName, argsStr string) string {
	switch toolName {
	case "web_search":
		return unquoteJSONString(argsStr, "query")
	case "web_fetch", "web_reader", "mcp__web-reader__webReader":
		return unquoteJSONString(argsStr, "url")
	}
	return ""
}

// unquoteJSONString extracts a top-level-ish string field value from
// compact JSON without a full Unmarshal (arguments shapes vary across
// providers; tolerant parsing beats hard failure).
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
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// recordFinding appends one finding to the overseer's collection. Called
// from the tool-result assembly throat AFTER redaction (#1195): snippets
// never hold unredacted secrets. Findings are advisory synthesis input,
// never persisted to session history.
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
	if m := findingURLRe.FindString(resultContent); m != "" {
		url = m
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

// findingsSnapshot returns a copy of collected findings (for the gate).
func (o *overseerState) findingsSnapshot() []ResearchFinding {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ResearchFinding, len(o.researchFindings))
	copy(out, o.researchFindings)
	return out
}

// hostOf extracts the scheme-stripped host of a URL, best-effort.
func hostOf(url string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// SynthesizeResearchReport folds collected findings into the four-section
// draft the r365 gate demands: per-sub-query findings with numbered
// sources, conflict candidates (divergent hosts within one group), and
// gaps (sub-queries with zero findings). Pure function, zero I/O. Returns
// "" when there is nothing to fold.
func SynthesizeResearchReport(goal string, findings []ResearchFinding, planned []ResearchSubQuery) string {
	if len(findings) == 0 {
		return ""
	}
	// Group by normalized query (lowercased, whitespace-collapsed).
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

	// Gaps: planned sub-queries with no finding whose query text overlaps.
	if len(planned) > 0 {
		joined := strings.ToLower(strings.Join(groups, " \n "))
		for _, p := range planned {
			pk := strings.Join(strings.Fields(strings.ToLower(p.Query)), " ")
			if pk != "" && !strings.Contains(joined, firstWords(pk, gapProbeWords)) {
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
	_ = goal // goal reserved for future relevance ranking; draft is complete without it
	return b.String()
}

// gapProbeWords is how many leading words of a planned sub-query are used
// as the containment probe against collected finding queries.
const gapProbeWords = 3

// firstWords returns the first n words of a normalized string, used as a
// cheap containment probe for gap detection.
func firstWords(s string, n int) string {
	ws := strings.Fields(s)
	if len(ws) > n {
		ws = ws[:n]
	}
	return strings.Join(ws, " ")
}
