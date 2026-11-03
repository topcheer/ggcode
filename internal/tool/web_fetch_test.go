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

func TestWebFetchTruncationRuneSafe(t *testing.T) {

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
