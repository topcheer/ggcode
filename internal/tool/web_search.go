package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebSearch implements the web_search tool — searches using DuckDuckGo HTML.
type WebSearch struct{}

func (t WebSearch) Name() string { return "web_search" }
func (t WebSearch) Description() string {
	return "Search the web using DuckDuckGo. Returns a list of results with title, URL, and snippet."
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
			}
		},
		"required": ["query"]
	}`)
}

func (t WebSearch) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
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

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("search request failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("read body failed: %v", err)}, nil
	}

	results := parseDDGResults(string(body), args.MaxResults)
	if len(results) == 0 {
		return Result{Content: "No results found."}, nil
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}
	return Result{Content: sb.String()}, nil
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func parseDDGResults(htmlBody string, max int) []searchResult {
	// DuckDuckGo HTML wraps each result in a <div class="result">
	// with <a class="result__a"> for title/link and <a class="result__snippet"> for snippet.
	reResult := regexp.MustCompile(`(?is)<a[^>]+class="result__a"[^>]*>(.*?)</a>`)
	reSnippet := regexp.MustCompile(`(?is)<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
	reHref := regexp.MustCompile(`href="(https?://[^"]+)"`)

	resultBlocks := regexp.MustCompile(`(?is)<div[^>]+class="result[^"]*"[^>]*>`)
	indices := resultBlocks.FindAllStringIndex(htmlBody, max)

	var results []searchResult
	for _, idx := range indices {
		block := htmlBody[idx[0]:]
		// Limit block to next result div or end
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
				resultURL = hrefMatch[1]
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
