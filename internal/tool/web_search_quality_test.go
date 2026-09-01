package tool

import (
	"fmt"
	"testing"
)

func TestAssessSearchResults_SpamFiltering(t *testing.T) {
	results := []searchResult{
		{Title: "Go Tutorial", URL: "https://w3schools.com/go", Snippet: "Learn Go"},
		{Title: "Go Docs", URL: "https://go.dev/doc/", Snippet: "Official Go documentation"},
	}
	out := assessSearchResults("go tutorial", results, nil)
	if len(out) != 1 {
		t.Fatalf("expected 1 result after spam filter, got %d", len(out))
	}
	if out[0].URL != "https://go.dev/doc/" {
		t.Errorf("expected go.dev result, got %s", out[0].URL)
	}
}

func TestAssessSearchResults_TypeClassification(t *testing.T) {
	cases := []struct {
		url      string
		wantType string
	}{
		{"https://github.com/golang/go", "repo"},
		{"https://pkg.go.dev/encoding/json", "package"},
		{"https://stackoverflow.com/q/12345", "QA"},
		{"https://developer.mozilla.org/en-US/docs/Web", "docs"},
		{"https://npmjs.com/package/express", "package"},
		{"https://medium.com/some-blog", "blog"},
		{"https://youtube.com/watch?v=abc", "video"},
		{"https://arxiv.org/abs/2504.12345", "academic"},
	}
	for _, tc := range cases {
		got := classifyResultType(tc.url, domainFromURL(tc.url))
		if got != tc.wantType {
			t.Errorf("classifyResultType(%s) = %q, want %q", tc.url, got, tc.wantType)
		}
	}
}

func TestAssessSearchResults_TypeTagPrepended(t *testing.T) {
	results := []searchResult{
		{Title: "Go Repo", URL: "https://github.com/golang/go", Snippet: "The Go repo"},
	}
	out := assessSearchResults("go repo", results, nil)
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	if out[0].Title != "[repo] Go Repo" {
		t.Errorf("expected title with [repo] tag, got %q", out[0].Title)
	}
}

func TestAssessSearchResults_DomainDeduplication(t *testing.T) {
	results := []searchResult{
		{Title: "Result 1", URL: "https://stackoverflow.com/q/1", Snippet: "answer 1"},
		{Title: "Result 2", URL: "https://stackoverflow.com/q/2", Snippet: "answer 2"},
		{Title: "Result 3", URL: "https://stackoverflow.com/q/3", Snippet: "answer 3"},
		{Title: "Result 4", URL: "https://go.dev/doc", Snippet: "go docs"},
	}
	out := assessSearchResults("answer", results, nil)
	// StackOverflow should be capped at maxResultsPerDomain (2)
	soCount := 0
	for _, r := range out {
		if domainFromURL(r.URL) == "stackoverflow.com" {
			soCount++
		}
	}
	if soCount != maxResultsPerDomain {
		t.Errorf("expected %d stackoverflow results, got %d", maxResultsPerDomain, soCount)
	}
}

func TestAssessSearchResults_RelevanceScoring(t *testing.T) {
	results := []searchResult{
		{Title: "Unrelated Article", URL: "https://example.com/a", Snippet: "Something about cats"},
		{Title: "Go context package guide", URL: "https://example.com/b", Snippet: "context.Background usage in Go"},
	}
	out := assessSearchResults("go context package", results, nil)
	// The more relevant result should be ranked first
	if len(out) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out))
	}
	if out[0].Title != "Go context package guide" {
		t.Errorf("expected relevant result first, got %q", out[0].Title)
	}
}

func TestAssessSearchResults_EmptyInput(t *testing.T) {
	out := assessSearchResults("anything", nil, nil)
	if len(out) != 0 {
		t.Errorf("expected 0 results for nil input, got %d", len(out))
	}
}

func TestTokenizeQuery(t *testing.T) {
	terms := tokenizeQuery("how to parse JSON in Go")
	// "how", "to", "in" are stop words; should keep "parse", "json", "go"
	want := []string{"parse", "json", "go"}
	if len(terms) != len(want) {
		t.Fatalf("expected %d terms, got %d: %v", len(want), len(terms), terms)
	}
	for i, w := range want {
		if terms[i] != w {
			t.Errorf("term[%d] = %q, want %q", i, terms[i], w)
		}
	}
}

func TestScoreResult(t *testing.T) {
	queryTerms := []string{"go", "context", "cancel"}
	// High score: all terms in title
	high := scoreResult(queryTerms, searchResult{
		Title:   "Go context cancel guide",
		Snippet: "Using context.Cancel in Go",
	})
	// Low score: no terms in title or snippet
	low := scoreResult(queryTerms, searchResult{
		Title:   "Cat pictures gallery",
		Snippet: "Funny cats and dogs",
	})
	if high <= low {
		t.Errorf("expected high score > low score, got high=%d low=%d", high, low)
	}
	if high < 50 {
		t.Errorf("expected high score >= 50, got %d", high)
	}
	if low > 20 {
		t.Errorf("expected low score <= 20, got %d", low)
	}
}

func TestScoreResult_NoQueryTerms(t *testing.T) {
	score := scoreResult(nil, searchResult{
		Title: "Anything", Snippet: "Whatever",
	})
	if score != 50 {
		t.Errorf("expected neutral score 50 for no query terms, got %d", score)
	}
}

func TestIsSpamDomain(t *testing.T) {
	spam := []string{
		"w3schools.com",
		"www.w3schools.com",
		"sub.w3schools.com",
		"tutorialspoint.com",
		"geeksforgeeks.org",
	}
	for _, d := range spam {
		if !isSpamDomain(d) {
			t.Errorf("expected %s to be spam", d)
		}
	}

	clean := []string{
		"go.dev",
		"github.com",
		"stackoverflow.com",
		"pkg.go.dev",
	}
	for _, d := range clean {
		if isSpamDomain(d) {
			t.Errorf("expected %s to NOT be spam", d)
		}
	}
}

func TestAssessSearchResults_DiversityAfterDedup(t *testing.T) {
	// 5 results from same domain + 2 from another
	results := []searchResult{
		{Title: "A1", URL: "https://example.com/1", Snippet: "test a"},
		{Title: "A2", URL: "https://example.com/2", Snippet: "test a"},
		{Title: "A3", URL: "https://example.com/3", Snippet: "test a"},
		{Title: "A4", URL: "https://example.com/4", Snippet: "test a"},
		{Title: "A5", URL: "https://example.com/5", Snippet: "test a"},
		{Title: "B1", URL: "https://other.com/1", Snippet: "test b"},
		{Title: "B2", URL: "https://other.com/2", Snippet: "test b"},
	}
	out := assessSearchResults("test", results, nil)
	// Should be maxResultsPerDomain (2) from example.com + 2 from other.com = 4
	if len(out) != 4 {
		t.Errorf("expected 4 results after dedup, got %d", len(out))
	}
}

// TestAssessSearchResultsAllowedDomainExemptFromDedup pins #1357: the user's
// explicit allowed_domains must override the per-domain diversity cap - a
// single-domain filter previously left only maxResultsPerDomain=2 results
// no matter how many were requested or prefetched.
func TestAssessSearchResultsAllowedDomainExemptFromDedup(t *testing.T) {
	var results []searchResult
	for i := 0; i < 8; i++ {
		results = append(results, searchResult{
			Title:   fmt.Sprintf("Python doc page %d", i),
			URL:     fmt.Sprintf("https://docs.python.org/3/library/page%d.html", i),
			Snippet: "python documentation page",
		})
	}
	out := assessSearchResults("python stdlib", results, []string{"docs.python.org"})
	if len(out) != 8 {
		t.Fatalf("expected all 8 allowed-domain results to survive the dedup cap, got %d", len(out))
	}

	// Without the exemption the diversity cap applies as before.
	out = assessSearchResults("python stdlib", results, nil)
	if len(out) != maxResultsPerDomain {
		t.Fatalf("expected diversity cap of %d without exemption, got %d", maxResultsPerDomain, len(out))
	}
}

// TestAssessSearchResultsExemptionNormalization pins #1406-B: an agent
// passing "https://example.com" (or "example.com.") as an allowed domain
// passed filterByDomain but MISSED the exemption key (weaker normalization
// than the filter), so #1357's single-domain guarantee silently regressed
// - 5 same-domain results cut to 2 despite the 3x prefetch. Both sides
// share normalizeDomains now.
func TestAssessSearchResultsExemptionNormalization(t *testing.T) {
	results := make([]searchResult, 5)
	for i := range results {
		results[i] = searchResult{Title: fmt.Sprintf("r%d", i), URL: fmt.Sprintf("https://example.com/page%d", i)}
	}
	for _, domain := range []string{"https://example.com", "example.com.", "example.com"} {
		out := assessSearchResults("q", results, []string{domain})
		if len(out) != 5 {
			t.Errorf("allowedDomains=%q: exemption missed, got %d results (want 5)", domain, len(out))
		}
	}
}
