package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExtractURLs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"no url", "hello world", 0},
		{"one url", "check https://example.com please", 1},
		{"two urls", "see https://a.com and https://b.com", 2},
		{"duplicate urls", "https://a.com https://a.com again", 1},
		{"url with trailing punctuation", "see https://example.com.", 1},
		{"url in parentheses", "(https://example.com)", 1},
		{"url with path", "https://example.com/path/to/page?query=1", 1},
		{"max cap", "https://a.com https://b.com https://c.com https://d.com", 3},
		{"no scheme", "visit example.com today", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls := extractURLs(tt.input)
			if len(urls) != tt.want {
				t.Errorf("extractURLs(%q) = %v, want %d urls", tt.input, urls, tt.want)
			}
		})
	}
}

func TestExtractURLsTrailingPunctuation(t *testing.T) {
	urls := extractURLs("check https://example.com/path.,;")
	if len(urls) != 1 {
		t.Fatalf("expected 1 url, got %d", len(urls))
	}
	if strings.HasSuffix(urls[0], ".,;") {
		t.Errorf("trailing punctuation not stripped: %q", urls[0])
	}
}

func TestExpandURLsNoURL(t *testing.T) {
	input := "hello world, no URLs here"
	result := ExpandURLs(context.Background(), input)
	if result != input {
		t.Errorf("expected unchanged input, got %q", result)
	}
}

func TestExpandURLsFetchSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><p>Hello World</p></body></html>"))
	}))
	defer ts.Close()

	result := expandURLsWithOpts(context.Background(), "check this out: "+ts.URL, true)
	if !strings.Contains(result, "Hello World") {
		t.Errorf("expected fetched content in result, got: %s", result)
	}
	if !strings.Contains(result, "[Fetched URL:") {
		t.Errorf("expected [Fetched URL:] marker, got: %s", result)
	}
}

func TestExpandURLsFetchError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer ts.Close()

	result := expandURLsWithOpts(context.Background(), "check: "+ts.URL, true)
	if !strings.Contains(result, "[Fetched URL:") {
		t.Errorf("expected [Fetched URL:] marker even on error, got: %s", result)
	}
	if !strings.Contains(result, "error:") {
		t.Errorf("expected error annotation, got: %s", result)
	}
}

func TestExpandURLsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := ExpandURLs(ctx, "check https://example.com")
	if !strings.Contains(result, "check https://example.com") {
		t.Errorf("expected original text preserved, got: %s", result)
	}
}

func TestExpandURLsMultiple(t *testing.T) {
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<p>Page One</p>"))
	}))
	defer ts1.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<p>Page Two</p>"))
	}))
	defer ts2.Close()

	input := "compare " + ts1.URL + " and " + ts2.URL
	result := expandURLsWithOpts(context.Background(), input, true)
	if !strings.Contains(result, "Page One") {
		t.Errorf("expected Page One content, got: %s", result)
	}
	if !strings.Contains(result, "Page Two") {
		t.Errorf("expected Page Two content, got: %s", result)
	}
}

func TestExpandURLsPreservesOriginalText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<p>content</p>"))
	}))
	defer ts.Close()

	original := "Please review " + ts.URL + " and tell me"
	result := expandURLsWithOpts(context.Background(), original, true)
	if !strings.HasPrefix(result, original) {
		t.Errorf("expected original text at start, got: %s", result)
	}
}

func TestExpandURLsTimeout(t *testing.T) {
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until test is done so server.Close() doesn't hang
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer ts.Close()
	defer close(done)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := expandURLsWithOpts(ctx, "check: "+ts.URL, true)
	if !strings.Contains(result, "[Fetched URL:") {
		t.Errorf("expected marker with error, got: %s", result)
	}
}

// TestExtractURLsCJKAndParens pins #1397: CJK text glued to a URL must not
// be swallowed into the match, and Wikipedia-style parenthesized paths must
// survive intact while prose parens are stripped.
func TestExtractURLsCJKAndParens(t *testing.T) {
	// A: CJK suffix stays OUT of the URL.
	got := extractURLs("看 https://example.com/docs这个文档")
	if len(got) != 1 || got[0] != "https://example.com/docs" {
		t.Fatalf("CJK swallowed or wrong extraction: %#v", got)
	}

	// B: balanced parens (Wikipedia) survive whole.
	got = extractURLs("see https://en.wikipedia.org/wiki/Python_(programming_language) now")
	if len(got) != 1 || got[0] != "https://en.wikipedia.org/wiki/Python_(programming_language)" {
		t.Fatalf("paren path truncated: %#v", got)
	}

	// B2: prose closing paren is stripped (prose case).
	got = extractURLs("(see https://example.com/page)")
	if len(got) != 1 || got[0] != "https://example.com/page" {
		t.Fatalf("prose paren not stripped: %#v", got)
	}

	// Regression: trailing sentence punctuation still stripped.
	got = extractURLs("go to https://example.com/a, then rest.")
	if len(got) != 1 || got[0] != "https://example.com/a" {
		t.Fatalf("punctuation handling regressed: %#v", got)
	}
}

// TestExtractURLsClosingDelimiters pins #1419 (a #1397 regression): the
// [!-~] body charset admitted closing delimiters the old negated class
// excluded, so URLs were fetched with a trailing " ' > or ] verbatim -
// doomed fetches injecting 404/error pages into context. They are now
// stripped by trimURLTail like trailing punctuation.
func TestExtractURLsClosingDelimiters(t *testing.T) {
	cases := []struct{ in, want string }{
		{`看 "https://example.com/docs" 这段`, "https://example.com/docs"},
		{`'https://example.com/b'`, "https://example.com/b"},
		{"<https://example.com/markdown>", "https://example.com/markdown"},
		{"[link] https://example.com/l]", "https://example.com/l"},
		{`(see https://example.com/x)`, "https://example.com/x"},
	}
	for _, c := range cases {
		got := extractURLs(c.in)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("extractURLs(%q) = %#v, want [%q]", c.in, got, c.want)
		}
	}
}

// TestMaskAPIKey pins #1410: the byte-length gate + rune-count arithmetic
// computed negative Repeat counts for multi-byte keys (15 bytes / 5 runes)
// and panicked the TUI. All measures are runes now.
func TestMaskAPIKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "****"},
		{"short", "****"},          // 5 runes
		{"12345678", "****"},       // exactly 8 runes
		{"123456789", "1234*6789"}, // 9 runes
		{"0123456789abcdef", "0123********cdef"},
		{"密密密密密", "****"},              // 15 bytes / 5 runes - used to panic
		{"ab中文de", "****"},             // 9 bytes / 6 runes - used to panic
		{"12345678中文字", "1234***8中文字"}, // 11 runes: first 4 + 3 stars + last 4 (incl. '8')
	}
	for _, c := range cases {
		if got := maskAPIKey(c.in); got != c.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
