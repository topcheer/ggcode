package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WebSearch struct{}

func (t WebSearch) Name() string { return "web_search" }
func (t WebSearch) Description() string {
	return "Search the web using DuckDuckGo. Returns search result snippets, not full page contents. Use web_fetch on a selected result URL when you need the page text."
}

func (t WebSearch) ToolMeta() ToolMeta {
	return ToolMeta{
		CostEstimate: 0.0001,
		RateLimitRPS: 2.0,
		RetryPolicy:  "exponential",
		MaxRetries:   3,
	}
}

func (t WebSearch) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {
			"type": "string",
			"description": "The search query"
		},
		"max_results": {
			"type": "integer",
			"description": "Maximum number of results (default 5, max 10)"
		},
		"allowed_domains": {
			"type": "array",
			"items": {
				"type": "string"
			},
			"description": "Only include results from these domains. Matches the exact host and its subdomains (e.g. example.com also matches docs.example.com)."
		},
		"blocked_domains": {
			"type": "array",
			"items": {
				"type": "string"
			},
			"description": "Never include results from these domains. Matches the exact host and its subdomains."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"query",
		"description"
	]
}`)
}

func (t WebSearch) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Query          string   `json:"query"`
		MaxResults     int      `json:"max_results"`
		AllowedDomains []string `json:"allowed_domains"`
		BlockedDomains []string `json:"blocked_domains"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if args.Query == "" {
		return Result{IsError: true, Content: "query is required"}, nil
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 5
	}
	if args.MaxResults > 10 {
		args.MaxResults = 10
	}

	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(args.Query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid request: %v", err)}, nil
	}
	req.Header.Set("User-Agent", "ggcode/1.0 (web search tool)")

	client := util.NewInsecureAwareClient(15 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("search request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, err := util.ReadAll(resp.Body, util.ReadLimitGeneral)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("read body failed: %v", err)}, nil
	}

	fetchLimit := args.MaxResults
	if len(args.AllowedDomains) > 0 || len(args.BlockedDomains) > 0 {
		fetchLimit = args.MaxResults * 3
		if fetchLimit > 30 {
			fetchLimit = 30
		}
	}

	results := parseDDGResults(string(body), fetchLimit)

	results = filterByDomain(results, args.AllowedDomains, args.BlockedDomains)

	results, bestScore := assessSearchResultsScored(args.Query, results, args.AllowedDomains)

	total := len(results)
	if len(results) > args.MaxResults {
		results = results[:args.MaxResults]
	}

	if len(results) == 0 {
		return Result{Content: "No results found."}, nil
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	if total > len(results) {
		sb.WriteString(fmt.Sprintf("... [showing %d of %d filtered results — refine allowed_domains or raise max_results to see the rest]\n", len(results), total))
	}

	if bestScore < weakSearchBestScore {
		sb.WriteString(fmt.Sprintf("[search-quality] best result matches only %d%% of query terms - results look weak. Consider rephrasing with vocabulary the target docs actually use, or narrowing via allowed_domains.\n", bestScore))
	}
	return Result{Content: sb.String()}, nil
}

const weakSearchBestScore = 30

func filterByDomain(results []searchResult, allowedDomains, blockedDomains []string) []searchResult {
	if len(allowedDomains) == 0 && len(blockedDomains) == 0 {
		return results
	}

	allowed := normalizeDomains(allowedDomains)
	blocked := normalizeDomains(blockedDomains)

	var filtered []searchResult
	for _, r := range results {
		host := strings.ToLower(domainFromURL(r.URL))

		if len(blocked) > 0 && domainMatchesAny(host, blocked) {
			continue
		}
		if len(allowed) > 0 && !domainMatchesAny(host, allowed) {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

func normalizeDomains(domains []string) []string {
	out := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, d := range domains {
		d = strings.TrimSpace(strings.ToLower(d))
		d = strings.TrimPrefix(d, "http://")
		d = strings.TrimPrefix(d, "https://")
		d = strings.TrimSuffix(strings.Split(d, "/")[0], ".")

		d = strings.TrimPrefix(d, "www.")
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func domainMatchesAny(host string, domains []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func domainFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func parseDDGResults(htmlBody string, max int) []searchResult {

	reResult := regexp.MustCompile(`(?is)<a[^>]+class="result__a"[^>]*>(.*?)</a>`)
	reSnippet := regexp.MustCompile(`(?is)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	reHref := regexp.MustCompile(`href="([^"]+)"`)

	resultBlocks := regexp.MustCompile(`(?is)<div[^>]+class="result[\s"][^>]*>`)
	indices := resultBlocks.FindAllStringIndex(htmlBody, max)

	var results []searchResult
	for i, idx := range indices {

		block := htmlBody[idx[0]:]
		if i+1 < len(indices) {
			block = htmlBody[idx[0]:indices[i+1][0]]
		}
		if len(results) >= max {
			break
		}

		titleMatch := reResult.FindStringSubmatch(block)
		snippetMatch := reSnippet.FindStringSubmatch(block)

		var title, snippet, resultURL string
		if len(titleMatch) > 1 {
			title = StripHTML(titleMatch[1])
			hrefMatch := reHref.FindStringSubmatch(titleMatch[0])
			if len(hrefMatch) > 1 {
				resultURL = normalizeDDGResultURL(hrefMatch[1])
			}
		}
		if len(snippetMatch) > 1 {
			snippet = StripHTML(snippetMatch[1])
		}

		if title != "" {
			results = append(results, searchResult{
				Title:   title,
				URL:     resultURL,
				Snippet: snippet,
			})
		}
	}
	return results
}

func normalizeDDGResultURL(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "&amp;", "&"))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		if u, err := url.Parse(raw); err == nil {
			if uddg := u.Query().Get("uddg"); uddg != "" {
				return uddg
			}
		}
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		if u, err := url.Parse(raw); err == nil {
			if uddg := u.Query().Get("uddg"); uddg != "" {
				return uddg
			}
		}
	}
	return raw
}
