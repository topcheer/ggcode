package tool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestWebFetchTruncationRuneSafe pins #1353: truncation must be rune-based,
// not byte-based - a 20000-byte cut through CJK content produced invalid
// UTF-8 and shrank Chinese pages to ~6600 chars against the documented 20000.
func TestWebFetchTruncationRuneSafe(t *testing.T) {
	// ~30000 CJK chars (90000 bytes): well past the cap in BOTH units, so
	// rune-based truncation keeps 20000 chars while byte-based would cut
	// at 20000 bytes (6600 chars) mid-rune.
	page := "<html><body>" + strings.Repeat("汉", 30000) + "</body></html>"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, page)
	}))
	defer ts.Close()

	tool := WebFetch{AllowPrivate: true}
	res, err := tool.Execute(context.Background(), []byte(fmt.Sprintf(`{"url": %q, "description": "t"}`, ts.URL)))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !utf8.ValidString(res.Content) {
		t.Fatal("truncated output is not valid UTF-8 (byte-based cut)")
	}
	shown := utf8.RuneCountInString(strings.TrimSuffix(strings.Split(res.Content, "\n\n... [truncated:")[0], "\n"))
	if shown != 20000 {
		t.Errorf("expected exactly 20000 runes shown, got %d", shown)
	}
}
