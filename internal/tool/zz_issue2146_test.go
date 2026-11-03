package tool

import (
	"strings"
	"testing"
)

func TestIssue2146_SpamExemptionMatchesWwwAndSubdomains(t *testing.T) {
	results := []searchResult{
		{URL: "https://www.w3schools.com/go/tutorial.asp", Title: "Go Tutorial"},
		{URL: "https://learn.geeksforgeeks.org/go-basics", Title: "Go Basics"},
		{URL: "https://go.dev/doc/", Title: "Go Docs"},
	}
	out := assessSearchResults("go tutorial", results, []string{"w3schools.com", "geeksforgeeks.org"})
	if len(out) != 3 {
		t.Fatalf("allowed www + subdomain results must all survive the spam phase, got %d: %+v", len(out), out)
	}

	out = assessSearchResults("go tutorial", results, nil)
	for _, r := range out {
		if strings.Contains(r.URL, "w3schools") || strings.Contains(r.URL, "geeksforgeeks") {
			t.Fatalf("spam domains must stay filtered without allowed_domains: %s", r.URL)
		}
	}
}

func TestIssue2146_ExtractByIDDoesNotTruncateAtFirstChild(t *testing.T) {
	long := "<p>" + strings.Repeat("word ", 60) + "</p>"
	html := `<html><body><div id="content"><h1>Short title</h1>` + long + `<p>tail paragraph with plenty of words to survive the length floor check</p></div></body></html>`
	got := extractByID(html, "content")
	if got == "" {
		t.Fatal("extractByID must capture the whole container, not fail on the short first child")
	}
	if !strings.Contains(got, "tail paragraph") {
		t.Fatalf("capture truncated before the container end: %q", got[:zz2146min(120, len(got))])
	}
	if !strings.Contains(got, "Short title") {
		t.Fatal("capture must include the first child")
	}
}

func TestIssue2146_ExtractAttrMatchNestedSameName(t *testing.T) {
	long := "<span>" + strings.Repeat("x", 150) + "</span>"
	html := `<body><div role="main"><nav>menu</nav>` + long + `<section>` + long + `</section></div></body>`
	got := extractAttrMatch(html, "role", "main")
	if got == "" {
		t.Fatal("extractAttrMatch must capture the main container")
	}
	if !strings.Contains(got, "section") {
		t.Fatal("capture truncated at the first child closing tag")
	}
}

func zz2146min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
