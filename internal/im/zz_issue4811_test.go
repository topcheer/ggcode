package im

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #4811: uploadFile must retry on 429 (and on ok=false "ratelimited" in a
// 200 body) like every sibling sender - a mixed text+image message used to
// silently lose the image on the first 429.
func TestIssue4811_UploadFileRetriesOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a := &slackAdapter{name: "sl", apiBase: srv.URL, httpClient: srv.Client(), botToken: "xoxb"}
	if err := a.uploadFile(context.Background(), "C1", "", "img.png", []byte("png"), ""); err != nil {
		t.Fatalf("#4811: uploadFile should succeed after one 429, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("#4811: expected 2 calls (1x429 + 1 success), got %d", calls)
	}
}

// #4811: exhausting the retry budget surfaces rateLimitExhausted instead of
// a generic ok=false error.
func TestIssue4811_UploadFileRateLimitExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	a := &slackAdapter{name: "sl", apiBase: srv.URL, httpClient: srv.Client(), botToken: "xoxb"}
	start := time.Now()
	err := a.uploadFile(context.Background(), "C1", "", "img.png", []byte("png"), "")
	if err == nil {
		t.Fatal("#4811: expected error after exhausting retries")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("#4811: retry backoff runaway: took %v", time.Since(start))
	}
}
