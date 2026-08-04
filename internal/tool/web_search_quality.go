package tool

// Web Search Result Quality Assessment - Deduplication, Relevance Scoring,
// and Result Type Classification.
//
// Trend: AI coding agents increasingly rely on web search for research,
// debugging, and API discovery. Raw search results from DuckDuckGo often
// contain:
//   - Duplicate domains (5 results from stackoverflow.com for one query)
//   - SEO spam and content farms
//   - Results ordered by DDG ranking, not by relevance to the agent's query
//   - No indication of result type (docs vs. blog vs. package registry)
//
// Competitor analysis:
//   - Perplexity/Phind: re-rank results by LLM relevance scoring
//   - Claude Code: no result quality processing (raw DDG output)
//   - Cursor: uses web results for code gen but no quality layer
//   - Cline/OpenHands: no web search at all
//   - Aider: no web search
//
// Gap: Results are returned raw. The agent must read all snippets and
// decide which to fetch, wasting context tokens on duplicates and
// low-quality results.
//
// Solution: Deterministic post-processing layer applied after parsing:
//   1. Filter: remove known spam/low-quality domains
//   2. Classify: tag each result with [docs], [package], [QA], [repo], etc.
//   3. Score: rank by query-term overlap with title (3x) and snippet (1x)
//   4. Deduplicate: cap results per domain (default 2) to ensure diversity
//   5. Enrich: annotate each result with type for agent decision-making
//
// Design:
//   - Zero external dependencies, pure Go string/token processing
//   - Non-destructive: filtering/dedup reduce results, scoring reorders them
//   - Type tags are short (4-8 chars) to minimize context overhead
//   - Scores are normalized 0-100 for human/agent readability

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// maxResultsPerDomain caps how many results can come from the same domain.
// This prevents one dominant site (e.g., 5 stackoverflow.com results)
// from crowding out diverse sources.
const maxResultsPerDomain = 2

// spamDomains are known low-quality / content-farm domains that add noise.
var spamDomains = map[string]bool{
	"w3schools.com":      true,
	"tutorialspoint.com": true,
	"programiz.com":      true,
	"javatpoint.com":     true,
	"geeksforgeeks.org":  true,
}

// scoredResult wraps a searchResult with quality metadata.
type scoredResult struct {
	searchResult
	score  int    // 0-100 relevance score
	rtype  string // result type tag (docs, package, QA, repo, etc.)
	domain string
}

// assessSearchResults applies quality processing to raw search results:
// spam filtering, type classification, relevance scoring, domain
// deduplication, stable sort by score. Returns results with type tags
// prepended to titles.
func assessSearchResults(query string, results []searchResult) []searchResult {
	if len(results) == 0 {
		return results
	}

	queryTerms := tokenizeQuery(query)

	// Phase 1: Filter spam domains
	var filtered []searchResult
	for _, res := range results {
		domain := domainFromURL(res.URL)
		if isSpamDomain(domain) {
			continue
		}
		filtered = append(filtered, res)
	}

	// Phase 2+3: Classify and score
	scored := make([]scoredResult, 0, len(filtered))
	for _, res := range filtered {
		domain := domainFromURL(res.URL)
		scored = append(scored, scoredResult{
			searchResult: res,
			score:        scoreResult(queryTerms, res),
			rtype:        classifyResultType(res.URL, domain),
			domain:       domain,
		})
	}

	// Phase 4: Deduplicate by domain (keep highest-scoring per domain)
	scored = deduplicateByDomain(scored)

	// Phase 5: Sort by score descending (stable to preserve original
	// DDG rank order for equal scores)
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Convert back to searchResult with type tag in title
	out := make([]searchResult, len(scored))
	for idx, sr := range scored {
		if sr.rtype != "" {
			out[idx] = searchResult{
				Title:   fmt.Sprintf("[%s] %s", sr.rtype, sr.Title),
				URL:     sr.URL,
				Snippet: sr.Snippet,
			}
		} else {
			out[idx] = sr.searchResult
		}
	}
	return out
}

// tokenizeQuery splits a search query into lowercase terms for scoring.
// Removes common stop words and short terms.
func tokenizeQuery(query string) []string {
	query = strings.ToLower(query)
	terms := strings.FieldsFunc(query, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '+' && r != '#' && r != '.'
	})

	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "to": true,
		"of": true, "in": true, "on": true, "at": true, "by": true,
		"for": true, "with": true, "from": true, "how": true, "do": true,
		"does": true, "what": true, "why": true, "when": true, "and": true,
		"or": true, "not": true, "this": true, "that": true, "it": true,
	}

	var result []string
	for _, term := range terms {
		if len(term) < 2 || stopWords[term] {
			continue
		}
		result = append(result, term)
	}
	return result
}

// scoreResult computes a 0-100 relevance score based on query term overlap
// with the title (weight 3x) and snippet (weight 1x).
func scoreResult(queryTerms []string, res searchResult) int {
	if len(queryTerms) == 0 {
		return 50 // neutral score when no query terms
	}

	titleLower := strings.ToLower(res.Title)
	snippetLower := strings.ToLower(res.Snippet)

	var titleHits, snippetHits int
	for _, term := range queryTerms {
		if strings.Contains(titleLower, term) {
			titleHits++
		}
		if strings.Contains(snippetLower, term) {
			snippetHits++
		}
	}

	// Weighted: title matches count 3x, snippet matches 1x
	rawScore := float64(titleHits)*3.0 + float64(snippetHits)*1.0
	maxPossible := float64(len(queryTerms)) * 4.0 // 3+1

	if maxPossible == 0 {
		return 50
	}

	normalized := (rawScore / maxPossible) * 100
	return int(math.Round(math.Max(0, math.Min(100, normalized))))
}

// classifyResultType determines the type tag for a URL based on domain/path.
func classifyResultType(rawURL, domain string) string {
	if domain == "" {
		return ""
	}

	// GitHub: repo vs. docs vs. issues
	if domain == "github.com" {
		return "repo"
	}

	// Package registries
	switch domain {
	case "npmjs.com", "www.npmjs.com":
		return "package"
	case "pypi.org":
		return "package"
	case "pkg.go.dev":
		return "package"
	case "crates.io":
		return "package"
	case "rubygems.org":
		return "package"
	case "pub.dev":
		return "package"
	case "maven.apache.org", "search.maven.org", "mvnrepository.com":
		return "package"
	case "hub.docker.com":
		return "image"
	}

	// Q&A sites
	switch domain {
	case "stackoverflow.com", "serverfault.com", "superuser.com",
		"askubuntu.com", "mathoverflow.net":
		return "QA"
	}

	// Documentation sites (docs.* subdomain pattern)
	if strings.HasPrefix(domain, "docs.") || strings.HasPrefix(domain, "doc.") {
		return "docs"
	}

	// Official language/framework docs
	knownDocs := map[string]bool{
		"developer.mozilla.org":     true,
		"react.dev":                 true,
		"vuejs.org":                 true,
		"angular.io":                true,
		"nodejs.org":                true,
		"python.org":                true,
		"go.dev":                    true,
		"rust-lang.org":             true,
		"typescriptlang.org":        true,
		"developer.apple.com":       true,
		"learn.microsoft.com":       true,
		"cloud.google.com":          true,
		"aws.amazon.com":            true,
		"kubernetes.io":             true,
		"docker.com":                true,
		"tailwindcss.com":           true,
		"nextjs.org":                true,
		"expressjs.com":             true,
		"fastapi.tiangolo.com":      true,
		"django-rest-framework.org": true,
	}
	if knownDocs[domain] {
		return "docs"
	}

	// Blog sites
	switch domain {
	case "medium.com", "dev.to", "hashnode.com", "freecodecamp.org",
		"css-tricks.com", "smashingmagazine.com":
		return "blog"
	}

	// Video
	if domain == "youtube.com" || domain == "www.youtube.com" {
		return "video"
	}

	// Academic
	if strings.HasSuffix(domain, ".edu") || domain == "arxiv.org" || domain == "scholar.google.com" {
		return "academic"
	}

	return ""
}

// isSpamDomain checks if a domain is a known low-quality/spam source.
func isSpamDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
	if spamDomains[domain] {
		return true
	}
	// Check parent domain for subdomains
	parts := strings.Split(domain, ".")
	if len(parts) > 2 {
		parent := strings.Join(parts[len(parts)-2:], ".")
		if spamDomains[parent] {
			return true
		}
	}
	return false
}

// deduplicateByDomain caps the number of results from the same domain.
// Keeps the highest-scoring results per domain (by index order, since
// input is in DDG rank order before scoring).
func deduplicateByDomain(scored []scoredResult) []scoredResult {
	if len(scored) <= maxResultsPerDomain {
		return scored
	}

	domainCount := make(map[string]int)
	result := make([]scoredResult, 0, len(scored))

	for _, sr := range scored {
		if sr.domain == "" {
			result = append(result, sr)
			continue
		}
		normalizedDomain := strings.TrimPrefix(sr.domain, "www.")
		if domainCount[normalizedDomain] < maxResultsPerDomain {
			domainCount[normalizedDomain]++
			result = append(result, sr)
		}
	}
	return result
}
