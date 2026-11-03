package tool

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const maxResultsPerDomain = 2

var spamDomains = map[string]bool{
	"w3schools.com":      true,
	"tutorialspoint.com": true,
	"programiz.com":      true,
	"javatpoint.com":     true,
	"geeksforgeeks.org":  true,
}

type scoredResult struct {
	searchResult
	score  int
	rtype  string
	domain string
}

func assessSearchResults(query string, results []searchResult, allowedDomains []string) []searchResult {
	out, _ := assessSearchResultsScored(query, results, allowedDomains)
	return out
}

func assessSearchResultsScored(query string, results []searchResult, allowedDomains []string) ([]searchResult, int) {

	allowed := make(map[string]bool, len(allowedDomains))
	for _, d := range normalizeDomains(allowedDomains) {
		allowed[d] = true
	}
	if len(results) == 0 {
		return results, 0
	}

	queryTerms := tokenizeQuery(query)

	var filtered []searchResult
	for _, res := range results {
		domain := domainFromURL(res.URL)
		if isSpamDomain(domain) {

			normalized := strings.TrimPrefix(domain, "www.")
			if !allowed[normalized] && !subdomainOfAny(normalized, allowed) {
				continue
			}
		}
		filtered = append(filtered, res)
	}

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

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})
	best := 0
	for _, s := range scored {
		if s.score > best {
			best = s.score
		}
	}
	scored = deduplicateByDomain(scored, allowed)

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
	return out, best
}

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

func containsTermAtBoundary(hay, term string) bool {
	if term == "" {
		return false
	}
	for i := 0; i+len(term) <= len(hay); i++ {
		if hay[i:i+len(term)] != term {
			continue
		}
		beforeOK := i == 0 || !isAlphaNumByte(hay[i-1])
		after := i + len(term)
		afterOK := after == len(hay) || !isAlphaNumByte(hay[after])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isAlphaNumByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func scoreResult(queryTerms []string, res searchResult) int {
	if len(queryTerms) == 0 {

		return 50
	}

	titleLower := strings.ToLower(res.Title)
	snippetLower := strings.ToLower(res.Snippet)

	var titleHits, snippetHits int
	for _, term := range queryTerms {

		if containsTermAtBoundary(titleLower, term) {
			titleHits++
		}
		if containsTermAtBoundary(snippetLower, term) {
			snippetHits++
		}
	}

	rawScore := float64(titleHits)*3.0 + float64(snippetHits)*1.0
	maxPossible := float64(len(queryTerms)) * 4.0

	if maxPossible == 0 {
		return 50
	}

	normalized := (rawScore / maxPossible) * 100
	return int(math.Round(math.Max(0, math.Min(100, normalized))))
}

func classifyResultType(rawURL, domain string) string {
	if domain == "" {
		return ""
	}

	if domain == "github.com" {
		return "repo"
	}

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

	switch domain {
	case "stackoverflow.com", "serverfault.com", "superuser.com",
		"askubuntu.com", "mathoverflow.net":
		return "QA"
	}

	if strings.HasPrefix(domain, "docs.") || strings.HasPrefix(domain, "doc.") {
		return "docs"
	}

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

	switch domain {
	case "medium.com", "dev.to", "hashnode.com", "freecodecamp.org",
		"css-tricks.com", "smashingmagazine.com":
		return "blog"
	}

	if domain == "youtube.com" || domain == "www.youtube.com" {
		return "video"
	}

	if strings.HasSuffix(domain, ".edu") || domain == "arxiv.org" || domain == "scholar.google.com" {
		return "academic"
	}

	return ""
}

func subdomainOfAny(host string, exempt map[string]bool) bool {
	for d := range exempt {
		if strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func isSpamDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
	if spamDomains[domain] {
		return true
	}

	parts := strings.Split(domain, ".")
	if len(parts) > 2 {
		parent := strings.Join(parts[len(parts)-2:], ".")
		if spamDomains[parent] {
			return true
		}
	}
	return false
}

func deduplicateByDomain(scored []scoredResult, exempt map[string]bool) []scoredResult {
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

		if exempt[normalizedDomain] || subdomainOfAny(normalizedDomain, exempt) || domainCount[normalizedDomain] < maxResultsPerDomain {
			domainCount[normalizedDomain]++
			result = append(result, sr)
		}
	}
	return result
}
